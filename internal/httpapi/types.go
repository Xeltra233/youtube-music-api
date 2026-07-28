package httpapi

import (
	"github.com/xeltra/ytmusic-bridge/internal/search"
)

// SearchRequestBody is the POST /search JSON body.
type SearchRequestBody struct {
	Query    string   `json:"query"`
	Limit    *int     `json:"limit"`
	MinScore *float64 `json:"min_score"`
}

// SearchResponseBody is the POST /search JSON response.
type SearchResponseBody struct {
	SessionID      string       `json:"session_id"`
	Query          string       `json:"query"`
	LimitRequested int          `json:"limit_requested"`
	LimitUsed      int          `json:"limit_used"`
	MinScoreUsed   float64      `json:"min_score_used"`
	Total          int          `json:"total"`
	Truncated      bool         `json:"truncated"`
	ExpiresIn      int          `json:"expires_in"`
	Results        []ResultItem `json:"results"`
}

// ResultItem is one bot-facing candidate track.
type ResultItem struct {
	Index           int      `json:"index"`
	DisplayName     string   `json:"display_name"`
	Title           string   `json:"title"`
	Artists         []string `json:"artists"`
	Album           string   `json:"album"`
	Duration        string   `json:"duration"`
	DurationSeconds int      `json:"duration_seconds"`
	VideoID         string   `json:"video_id"`
	Thumbnail       string   `json:"thumbnail"`
	MatchScore      float64  `json:"match_score"`
	// OfficialVideoID is the matched official music video id; empty when unknown.
	OfficialVideoID string `json:"official_video_id"`
	// OfficialVideoURL is a ready-to-send watch URL; empty when unknown.
	OfficialVideoURL string `json:"official_video_url"`
	// HasOfficialVideo is true when OfficialVideoID is non-empty.
	HasOfficialVideo bool `json:"has_official_video"`
}

// DownloadRequestBody is the POST /download JSON body.
type DownloadRequestBody struct {
	SessionID string `json:"session_id"`
	Index     int    `json:"index"`
	Name      string `json:"name"`
	VideoID   string `json:"video_id"`
	Format    string `json:"format"`
}

// DownloadJSONBody is the response for ?mode=json.
type DownloadJSONBody struct {
	Title           string   `json:"title"`
	Artists         []string `json:"artists"`
	DisplayName     string   `json:"display_name"`
	VideoID         string   `json:"video_id"`
	DurationSeconds int      `json:"duration_seconds"`
	Format          string   `json:"format"`
	Filesize        int64    `json:"filesize"`
	FileURL         string   `json:"file_url"`
	ExpiresIn       int      `json:"expires_in"`
	Cached          bool     `json:"cached"`
}

func itemToAPI(it search.Item) ResultItem {
	artists := it.Artists
	if artists == nil {
		artists = []string{}
	}
	return ResultItem{
		Index:            it.Index,
		DisplayName:      it.DisplayName,
		Title:            it.Title,
		Artists:          artists,
		Album:            it.Album,
		Duration:         it.Duration,
		DurationSeconds:  it.DurationSeconds,
		VideoID:          it.VideoID,
		Thumbnail:        it.Thumbnail,
		MatchScore:       it.MatchScore,
		OfficialVideoID:  it.OfficialVideoID,
		OfficialVideoURL: it.OfficialVideoURL,
		HasOfficialVideo: it.HasOfficialVideo,
	}
}

func itemsToAPI(items []search.Item) []ResultItem {
	out := make([]ResultItem, 0, len(items))
	for _, it := range items {
		out = append(out, itemToAPI(it))
	}
	return out
}
