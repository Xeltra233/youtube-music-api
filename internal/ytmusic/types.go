// Package ytmusic implements a minimal YouTube Music InnerTube client.
//
// It covers WEB_REMIX /youtubei/v1/search for songs and videos filters.
// Parsing is defensive: missing fields degrade to empty values and never panic.
package ytmusic

// Track is one song or music-video entry returned by Search / SearchFilter.
type Track struct {
	VideoID         string
	Title           string
	Artists         []string
	Album           string
	Duration        string
	DurationSeconds int
	Thumbnail       string
	// MusicVideoType comes from watchEndpointMusicConfig when present.
	// Examples: MUSIC_VIDEO_TYPE_ATV (song/audio track), MUSIC_VIDEO_TYPE_OMV (official music video).
	MusicVideoType string
}
