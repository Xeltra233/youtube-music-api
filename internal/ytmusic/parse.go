package ytmusic

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var durationPattern = regexp.MustCompile(`^(\d+:)*\d+:\d+$`)

// ParseSearchResponse walks an InnerTube search JSON body and extracts song tracks.
// Missing fields never panic; items without a videoId are skipped.
func ParseSearchResponse(body []byte) ([]Track, error) {
	if len(bytesTrimSpace(body)) == 0 {
		return nil, fmt.Errorf("ytmusic: empty response body")
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("ytmusic: invalid json: %w", err)
	}

	tracks := make([]Track, 0, 20)
	walkJSON(root, func(obj map[string]any) {
		raw, ok := obj["musicResponsiveListItemRenderer"]
		if !ok {
			return
		}
		renderer, ok := raw.(map[string]any)
		if !ok {
			return
		}
		track, ok := parseListItem(renderer)
		if !ok {
			return
		}
		tracks = append(tracks, track)
	})
	return tracks, nil
}

func parseListItem(renderer map[string]any) (Track, bool) {
	var t Track
	t.VideoID = firstNonEmpty(
		asString(dig(renderer, "playlistItemData", "videoId")),
		asString(dig(renderer, "overlay", "musicItemThumbnailOverlayRenderer", "content", "musicPlayButtonRenderer", "playNavigationEndpoint", "watchEndpoint", "videoId")),
		asString(dig(renderer, "flexColumns", 0, "musicResponsiveListItemFlexColumnRenderer", "text", "runs", 0, "navigationEndpoint", "watchEndpoint", "videoId")),
		asString(dig(renderer, "onTap", "watchEndpoint", "videoId")),
	)
	if t.VideoID == "" {
		return Track{}, false
	}

	t.Title = firstNonEmpty(
		joinRunTexts(asSlice(dig(renderer, "flexColumns", 0, "musicResponsiveListItemFlexColumnRenderer", "text", "runs"))),
		asString(dig(renderer, "flexColumns", 0, "musicResponsiveListItemFlexColumnRenderer", "text", "runs", 0, "text")),
	)

	metaRuns := collectMetaRuns(renderer)
	artists, album, duration := parseSongRuns(metaRuns)
	t.Artists = artists
	t.Album = album
	t.Duration = duration
	t.DurationSeconds = parseDurationSeconds(duration)
	t.Thumbnail = pickBestThumbnail(renderer)
	if t.Artists == nil {
		t.Artists = []string{}
	}
	return t, true
}

func collectMetaRuns(renderer map[string]any) []any {
	// Songs usually put artists/album/duration in flexColumns[1].
	// Some layouts split extra metadata into flexColumns[2]; only append non-views runs.
	out := make([]any, 0, 16)
	for _, idx := range []int{1, 2} {
		runs := asSlice(dig(renderer, "flexColumns", idx, "musicResponsiveListItemFlexColumnRenderer", "text", "runs"))
		if len(runs) == 0 {
			continue
		}
		// flexColumns[2] is often "1.2B plays"; skip pure view-count columns.
		if idx == 2 && looksLikeViewsOnly(runs) {
			continue
		}
		if len(out) > 0 {
			// Keep a separator so parseSongRuns' even/odd indexing stays stable.
			out = append(out, map[string]any{"text": " • "})
		}
		out = append(out, runs...)
	}
	return out
}

func looksLikeViewsOnly(runs []any) bool {
	if len(runs) == 0 {
		return false
	}
	for _, raw := range runs {
		run, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(asString(run["text"]))
		if text == "" || text == "•" || text == "·" || text == "• " || text == " · " {
			continue
		}
		lower := strings.ToLower(text)
		if strings.Contains(lower, "play") || strings.Contains(lower, "view") ||
			strings.Contains(text, "次") || strings.Contains(text, "播放") {
			return true
		}
		// bare number-ish view counts without duration pattern
		if !durationPattern.MatchString(text) {
			return true
		}
	}
	return false
}

func parseSongRuns(runs []any) (artists []string, album, duration string) {
	artists = make([]string, 0, 2)
	// Even indices are content; odd indices are separators (" • ").
	for i := 0; i < len(runs); i++ {
		if i%2 == 1 {
			continue
		}
		run, ok := runs[i].(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(asString(run["text"]))
		if text == "" {
			continue
		}
		if durationPattern.MatchString(text) {
			if duration == "" {
				duration = text
			}
			continue
		}
		// year
		if len(text) == 4 && isAllDigits(text) {
			continue
		}
		// views-ish tokens
		lower := strings.ToLower(text)
		if strings.Contains(lower, "play") || strings.Contains(lower, "view") ||
			strings.Contains(text, "次") || strings.Contains(text, "播放") {
			continue
		}

		browseID := asString(dig(run, "navigationEndpoint", "browseEndpoint", "browseId"))
		if browseID != "" && (strings.HasPrefix(browseID, "MPRE") || strings.Contains(browseID, "release_detail")) {
			if album == "" {
				album = text
			}
			continue
		}
		// Everything else with or without browseId is treated as an artist name.
		// Skip pure type labels that sometimes appear first (Song / Single / Album / Video).
		if isTypeLabel(text) {
			continue
		}
		artists = append(artists, text)
	}
	return artists, album, duration
}

func isTypeLabel(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "song", "songs", "single", "album", "video", "videos", "ep", "playlist":
		return true
	default:
		return false
	}
}

func parseDurationSeconds(duration string) int {
	duration = strings.TrimSpace(duration)
	if duration == "" || !durationPattern.MatchString(duration) {
		return 0
	}
	parts := strings.Split(duration, ":")
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

func pickBestThumbnail(renderer map[string]any) string {
	thumbs := asSlice(dig(renderer, "thumbnail", "musicThumbnailRenderer", "thumbnail", "thumbnails"))
	bestURL := ""
	bestArea := -1
	for _, raw := range thumbs {
		t, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		u := strings.TrimSpace(asString(t["url"]))
		if u == "" {
			continue
		}
		w := asInt(t["width"])
		h := asInt(t["height"])
		area := w * h
		if area >= bestArea {
			bestArea = area
			bestURL = u
		}
	}
	return bestURL
}

func walkJSON(v any, fn func(map[string]any)) {
	switch node := v.(type) {
	case map[string]any:
		fn(node)
		for _, child := range node {
			walkJSON(child, fn)
		}
	case []any:
		for _, child := range node {
			walkJSON(child, fn)
		}
	}
}

func dig(v any, path ...any) any {
	cur := v
	for _, key := range path {
		switch k := key.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur, ok = m[k]
			if !ok {
				return nil
			}
		case int:
			arr, ok := cur.([]any)
			if !ok || k < 0 || k >= len(arr) {
				return nil
			}
			cur = arr[k]
		default:
			return nil
		}
	}
	return cur
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		// JSON numbers decode as float64 by default; only accept integers.
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return ""
	default:
		return ""
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0
		}
		return int(n)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

func asSlice(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}

func joinRunTexts(runs []any) string {
	if len(runs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(runs))
	for _, raw := range runs {
		run, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text := asString(run["text"])
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
