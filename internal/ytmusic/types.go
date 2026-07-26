// Package ytmusic implements a minimal YouTube Music InnerTube client.
//
// It only covers song search (WEB_REMIX /youtubei/v1/search with the songs filter).
// Parsing is defensive: missing fields degrade to empty values and never panic.
package ytmusic

// Track is one song entry returned by Search.
type Track struct {
	VideoID         string
	Title           string
	Artists         []string
	Album           string
	Duration        string
	DurationSeconds int
	Thumbnail       string
}
