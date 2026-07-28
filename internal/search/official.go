package search

import (
	"strings"

	"github.com/xeltra/ytmusic-bridge/internal/matching"
	"github.com/xeltra/ytmusic-bridge/internal/ytmusic"
)

const (
	// officialVideoMinScore 是采纳官方视频候选的最低相似度。
	// 低于此值视为没有官方视频，避免把翻唱/无关 MV 错绑到歌曲上。
	officialVideoMinScore = 0.55
	// officialVideoMinTitleScore 要求标题本身也足够接近。
	// 否则同艺人不同歌（如 LADY vs Lemon）会因艺人 token 把 display_name 分抬高而误绑。
	officialVideoMinTitleScore = 0.50
	officialVideoURLPrefix     = "https://www.youtube.com/watch?v="
)

// attachOfficialVideos 为每条 song 结果填充官方 MV 字段（原地修改 items）。
// videos 为空或无法匹配时字段保持零值。
func attachOfficialVideos(items []Item, videos []ytmusic.Track) {
	if len(items) == 0 || len(videos) == 0 {
		return
	}
	candidates := selectOfficialCandidates(videos)
	if len(candidates) == 0 {
		return
	}
	for i := range items {
		id := bestOfficialVideoID(items[i], candidates)
		if id == "" {
			continue
		}
		items[i].OfficialVideoID = id
		items[i].OfficialVideoURL = officialVideoURLPrefix + id
		items[i].HasOfficialVideo = true
	}
}

// selectOfficialCandidates 优先返回 OMV；若完全没有 OMV，再放宽到非 ATV。
func selectOfficialCandidates(videos []ytmusic.Track) []ytmusic.Track {
	omv := make([]ytmusic.Track, 0, len(videos))
	other := make([]ytmusic.Track, 0, len(videos))
	seen := make(map[string]struct{}, len(videos))

	for _, v := range videos {
		id := strings.TrimSpace(v.VideoID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		typ := strings.ToUpper(strings.TrimSpace(v.MusicVideoType))
		switch typ {
		case "MUSIC_VIDEO_TYPE_OMV":
			omv = append(omv, v)
		case "MUSIC_VIDEO_TYPE_ATV":
			// 音轨条目不是“官方音乐视频”。
			continue
		default:
			// UGC / 空类型 / 未知：videos shelf 上可作降级候选。
			other = append(other, v)
		}
	}
	if len(omv) > 0 {
		return omv
	}
	return other
}

func bestOfficialVideoID(song Item, candidates []ytmusic.Track) string {
	bestID := ""
	bestScore := 0.0
	songDisplay := strings.TrimSpace(song.DisplayName)
	if songDisplay == "" {
		songDisplay = matching.BuildDisplayName(song.Title, song.Artists)
	}
	songTitle := strings.TrimSpace(song.Title)

	for _, v := range candidates {
		vid := strings.TrimSpace(v.VideoID)
		if vid == "" {
			continue
		}
		// 同一条 ATV/音轨 ID 不算“额外官方视频”。
		if song.VideoID != "" && vid == song.VideoID {
			continue
		}
		vDisplay := matching.BuildDisplayName(v.Title, v.Artists)
		vTitle := strings.TrimSpace(v.Title)
		displayScore := matching.MatchScore(songDisplay, vDisplay)
		titleScore := 0.0
		if songTitle != "" && vTitle != "" {
			titleScore = matching.MatchScore(songTitle, vTitle)
			// 标题太不像时直接跳过：禁止“只靠同歌手”误绑。
			if titleScore < officialVideoMinTitleScore {
				continue
			}
		}
		score := displayScore
		if titleScore > score {
			score = titleScore
		}
		// 严格大于：同分保留先出现的候选，结果与上游顺序稳定。
		if score > bestScore {
			bestScore = score
			bestID = vid
		}
	}
	if bestScore < officialVideoMinScore {
		return ""
	}
	return bestID
}
