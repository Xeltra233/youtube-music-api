// Package search 把上游 YouTube Music 原始结果整理成 bot 可消费的候选列表。
//
// 本层负责：limit 夹紧、display_name / match_score、min_score 过滤、截断、1-based index。
// session_id 由后续 session 层写入，不在本包处理。
package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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

	tracks, err := s.upstream.Search(ctx, query)
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
