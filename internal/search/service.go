// Package search 把上游 YouTube Music 原始结果整理成 bot 可消费的候选列表。
//
// 本层负责：limit 夹紧、display_name / match_score、min_score 过滤、截断、1-based index，
// 以及为每条结果尽量填充官方音乐视频字段（失败降级为空，不影响主搜索）。
// session_id 由后续 session 层写入，不在本包处理。
package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/xeltra/ytmusic-bridge/internal/config"
	"github.com/xeltra/ytmusic-bridge/internal/matching"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

// 可预期的请求错误，HTTP 层可映射为 400。
var (
	ErrEmptyQuery   = errors.New("search: empty query")
	ErrInvalidLimit = errors.New("search: limit must be >= 1")
)

// Upstream 是搜索上游（真实实现为 *ytmusic.Client，测试可 stub）。
type Upstream interface {
	Search(ctx context.Context, query string) ([]ytmusic.Track, error)
}

// VideoUpstream 可选：若实现，则并行拉取 videos 过滤结果用于官方 MV 匹配。
// *ytmusic.Client 已实现；旧 stub 未实现时官方视频字段保持为空。
type VideoUpstream interface {
	SearchFilter(ctx context.Context, query string, filter ytmusic.SearchFilter) ([]ytmusic.Track, error)
}

// Service 组合上游与配置，产出排序/过滤后的候选列表。
type Service struct {
	upstream Upstream
	cfg      *config.Config
}

// New 创建搜索服务。cfg 与 upstream 不可为 nil。
func New(upstream Upstream, cfg *config.Config) (*Service, error) {
	if upstream == nil {
		return nil, errors.New("search: nil upstream")
	}
	if cfg == nil {
		return nil, errors.New("search: nil config")
	}
	return &Service{upstream: upstream, cfg: cfg}, nil
}

// Request 是一次搜索请求。
//
// Limit 为 nil 表示未指定，使用服务端 DefaultLimit；
// 显式传入 <=0 返回 ErrInvalidLimit；> MaxLimit 会被夹到 MaxLimit。
// MinScore 为 nil 表示使用服务端默认 MinScore；>1 夹到 1；<0 视为未指定。
type Request struct {
	Query    string
	Limit    *int
	MinScore *float64
}

// Item 是返回给 bot 的一条候选。
type Item struct {
	Index           int
	DisplayName     string
	Title           string
	Artists         []string
	Album           string
	Duration        string
	DurationSeconds int
	VideoID         string
	Thumbnail       string
	MatchScore      float64
	// OfficialVideoID 是匹配到的官方音乐视频 ID；没有则为空。
	OfficialVideoID string
	// OfficialVideoURL 便于 bot 直接发送，形如 https://www.youtube.com/watch?v=...
	OfficialVideoURL string
	// HasOfficialVideo 等价于 OfficialVideoID != ""。
	HasOfficialVideo bool
}

// Response 是搜索服务结果（不含 session_id / expires_in，那是 G5 的职责）。
type Response struct {
	Query          string
	LimitRequested int
	LimitUsed      int
	MinScoreUsed   float64
	Total          int
	Truncated      bool
	Results        []Item
}

// Search 执行完整搜索管线。
func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	if s == nil {
		return nil, errors.New("search: nil service")
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, ErrEmptyQuery
	}

	limitRequested, limitUsed, err := s.resolveLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	minScoreUsed := s.resolveMinScore(req.MinScore)

	tracks, videos, err := s.fetchTracks(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search: upstream: %w", err)
	}

	scored := make([]Item, 0, len(tracks))
	for _, tr := range tracks {
		artists := tr.Artists
		if artists == nil {
			artists = []string{}
		} else {
			// 拷贝，避免调用方改到上游切片。
			artists = append([]string(nil), artists...)
		}
		display := matching.BuildDisplayName(tr.Title, artists)
		score := matching.MatchScore(query, display)
		if score < minScoreUsed {
			continue
		}
		scored = append(scored, Item{
			DisplayName:     display,
			Title:           tr.Title,
			Artists:         artists,
			Album:           tr.Album,
			Duration:        tr.Duration,
			DurationSeconds: tr.DurationSeconds,
			VideoID:         tr.VideoID,
			Thumbnail:       tr.Thumbnail,
			MatchScore:      score,
		})
	}

	// 高分在前；同分保持上游相对顺序（稳定排序）。
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].MatchScore > scored[j].MatchScore
	})

	truncated := len(scored) > limitUsed
	if len(scored) > limitUsed {
		scored = scored[:limitUsed]
	}

	attachOfficialVideos(scored, videos)

	for i := range scored {
		scored[i].Index = i + 1
	}
	if scored == nil {
		scored = []Item{}
	}

	return &Response{
		Query:          query,
		LimitRequested: limitRequested,
		LimitUsed:      limitUsed,
		MinScoreUsed:   minScoreUsed,
		Total:          len(scored),
		Truncated:      truncated,
		Results:        scored,
	}, nil
}

func (s *Service) fetchTracks(ctx context.Context, query string) (songs, videos []ytmusic.Track, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	type result struct {
		tracks []ytmusic.Track
		err    error
	}

	songCh := make(chan result, 1)
	go func() {
		tr, e := s.upstream.Search(ctx, query)
		songCh <- result{tracks: tr, err: e}
	}()

	var (
		videoTracks []ytmusic.Track
		videoErr    error
		wg          sync.WaitGroup
	)
	if vu, ok := s.upstream.(VideoUpstream); ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			videoTracks, videoErr = vu.SearchFilter(ctx, query, ytmusic.SearchFilterVideos)
		}()
	}

	songRes := <-songCh
	wg.Wait()

	if songRes.err != nil {
		return nil, nil, songRes.err
	}
	if videoErr != nil {
		// 官方视频是增强字段：videos 失败不拖垮主搜索。
		slog.Warn("search: videos upstream failed; official video fields left empty",
			"query", query,
			"err", videoErr,
		)
		videoTracks = nil
	}
	if songRes.tracks == nil {
		songRes.tracks = []ytmusic.Track{}
	}
	if videoTracks == nil {
		videoTracks = []ytmusic.Track{}
	}
	return songRes.tracks, videoTracks, nil
}

func (s *Service) resolveLimit(limit *int) (requested, used int, err error) {
	if limit == nil {
		used = s.cfg.DefaultLimit
		if used < 1 {
			used = 1
		}
		if used > s.cfg.MaxLimit {
			used = s.cfg.MaxLimit
		}
		return used, used, nil
	}
	if *limit < 1 {
		return 0, 0, ErrInvalidLimit
	}
	requested = *limit
	used = requested
	if used > s.cfg.MaxLimit {
		used = s.cfg.MaxLimit
	}
	return requested, used, nil
}

func (s *Service) resolveMinScore(minScore *float64) float64 {
	if minScore == nil {
		return s.cfg.ResolveMinScore(-1)
	}
	return s.cfg.ResolveMinScore(*minScore)
}
