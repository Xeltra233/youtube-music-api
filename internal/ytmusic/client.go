package ytmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://music.youtube.com/youtubei/v1/search"
	// Public WEB_REMIX API key used by music.youtube.com (same as ytmusicapi).
	defaultAPIKey = "AIzaSyC9XL3ZjWddXya6X74dJoCTL-WEYFDNX30"
	// songs filter: EgWKAQ + II + AWoMEA4QChADEAQQCRAF
	songsFilterParams = "EgWKAQIIAWoMEA4QChADEAQQCRAF"
	defaultUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
	defaultOrigin     = "https://music.youtube.com"
)

// Options configures a Client.
type Options struct {
	// HTTPClient is optional. When nil a keep-alive client with Timeout is built.
	HTTPClient *http.Client
	// Timeout is used only when HTTPClient is nil. Zero means 15s.
	Timeout time.Duration
	// Proxy is an optional HTTP(S) proxy URL, e.g. "http://127.0.0.1:7890".
	Proxy string
	// APIKey overrides the default WEB_REMIX key. Empty keeps the default.
	APIKey string
	// BaseURL overrides the search endpoint (useful for tests). Empty keeps the default.
	BaseURL string
	// UserAgent overrides the default browser UA.
	UserAgent string
	// HL / GL control language and country of the response text.
	HL string
	GL string
}

// Client talks to YouTube Music InnerTube search.
type Client struct {
	httpClient *http.Client
	apiKey     string
	baseURL    string
	userAgent  string
	hl         string
	gl         string
}

// New creates a reusable Client. The underlying HTTP client keeps connections alive.
func New(opts Options) (*Client, error) {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		if strings.TrimSpace(opts.Proxy) != "" {
			proxyURL, err := url.Parse(opts.Proxy)
			if err != nil {
				return nil, fmt.Errorf("invalid proxy %q: %w", opts.Proxy, err)
			}
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		httpClient = &http.Client{
			Timeout:   timeout,
			Transport: transport,
		}
	}

	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		apiKey = defaultAPIKey
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	userAgent := strings.TrimSpace(opts.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	hl := strings.TrimSpace(opts.HL)
	if hl == "" {
		hl = "en"
	}
	gl := strings.TrimSpace(opts.GL)
	if gl == "" {
		gl = "US"
	}

	return &Client{
		httpClient: httpClient,
		apiKey:     apiKey,
		baseURL:    baseURL,
		userAgent:  userAgent,
		hl:         hl,
		gl:         gl,
	}, nil
}

// Search posts a songs-filter query and returns the raw track list (typically ~20).
// Callers are responsible for limit / scoring / session (later layers).
func (c *Client) Search(ctx context.Context, query string) ([]Track, error) {
	if c == nil {
		return nil, fmt.Errorf("ytmusic: nil client")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("ytmusic: empty query")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	body := map[string]any{
		"context": map[string]any{
			"client": map[string]any{
				"clientName":    "WEB_REMIX",
				"clientVersion": clientVersion(time.Now().UTC()),
				"hl":            c.hl,
				"gl":            c.gl,
			},
		},
		"query":  query,
		"params": songsFilterParams,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: marshal request: %w", err)
	}

	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: invalid base url: %w", err)
	}
	q := endpoint.Query()
	if q.Get("alt") == "" {
		q.Set("alt", "json")
	}
	if q.Get("key") == "" {
		q.Set("key", c.apiKey)
	}
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", defaultOrigin)
	req.Header.Set("Cookie", "SOCS=CAI")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", c.hl)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ytmusic: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(respBody))
		if len(snippet) > 240 {
			snippet = snippet[:240] + "..."
		}
		return nil, fmt.Errorf("ytmusic: upstream status %d: %s", resp.StatusCode, snippet)
	}

	tracks, err := ParseSearchResponse(respBody)
	if err != nil {
		return nil, err
	}
	return tracks, nil
}

func clientVersion(now time.Time) string {
	return "1." + now.Format("20060102") + ".01.00"
}
