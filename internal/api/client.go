package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/version"
)

// Client is the LAEVITAS API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	Verbose    bool
}

// NewClient creates a new API client from config.
func NewClient(cfg *config.Config) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// APIError represents a structured error from the API.
type APIError struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
	Endpoint   string `json:"endpoint,omitempty"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// IsAuthError returns true for 401/403 responses.
func (e *APIError) IsAuthError() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

// IsRateLimit returns true for 429 responses.
func (e *APIError) IsRateLimit() bool {
	return e.StatusCode == http.StatusTooManyRequests
}

// NetworkError wraps connectivity failures with a user-friendly message.
type NetworkError struct {
	Err error
}

func (e *NetworkError) Error() string {
	return "Cannot reach api.laevitas.ch. Check your internet connection."
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

// Response wraps the raw API response with pagination info.
type Response struct {
	Data       json.RawMessage `json:"data"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// RequestParams holds common query parameters.
type RequestParams struct {
	Exchange       string
	InstrumentName string
	Currency       string
	Start          string
	End            string
	Resolution     string
	Limit          int
	Cursor         string

	// Options-specific
	Direction   string
	OptionType  string
	Maturity    string
	MinPremium  float64
	Sort        string
	SortDir     string
	BlockOnly   bool
	OpeningOnly bool

	// Predictions-specific
	Category  string
	EventSlug string
	Keyword   string

	// Snapshot-specific
	Date string

	// Extra allows arbitrary key-value params
	Extra map[string]string
}

// buildURL constructs the full request URL with query params.
func (c *Client) buildURL(path string, params *RequestParams) string {
	u := fmt.Sprintf("%s%s", c.baseURL, path)

	if params == nil {
		return u
	}

	q := url.Values{}

	if params.Exchange != "" {
		q.Set("exchange", params.Exchange)
	}
	if params.InstrumentName != "" {
		q.Set("instrument_name", params.InstrumentName)
	}
	if params.Currency != "" {
		q.Set("currency", params.Currency)
	}
	if params.Start != "" {
		q.Set("start", params.Start)
	}
	if params.End != "" {
		q.Set("end", params.End)
	}
	if params.Resolution != "" {
		q.Set("resolution", params.Resolution)
	}
	if params.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", params.Limit))
	}
	if params.Cursor != "" {
		q.Set("cursor", params.Cursor)
	}
	if params.Direction != "" {
		q.Set("direction", params.Direction)
	}
	if params.OptionType != "" {
		q.Set("option_type", params.OptionType)
	}
	if params.Maturity != "" {
		q.Set("maturity", params.Maturity)
	}
	if params.MinPremium > 0 {
		q.Set("min_premium_usd", fmt.Sprintf("%.0f", params.MinPremium))
	}
	if params.Sort != "" {
		q.Set("sort", params.Sort)
	}
	if params.SortDir != "" {
		q.Set("sort_dir", params.SortDir)
	}
	if params.BlockOnly {
		q.Set("block_only", "true")
	}
	if params.OpeningOnly {
		q.Set("opening_only", "true")
	}
	if params.Category != "" {
		q.Set("category", params.Category)
	}
	if params.EventSlug != "" {
		q.Set("event_slug", params.EventSlug)
	}
	if params.Keyword != "" {
		q.Set("keyword", params.Keyword)
	}
	if params.Date != "" {
		q.Set("date", params.Date)
	}

	for k, v := range params.Extra {
		q.Set(k, v)
	}

	if len(q) > 0 {
		return u + "?" + q.Encode()
	}
	return u
}

// isNetworkError checks if an error is a connectivity issue (DNS, TCP, timeout).
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// net.Error covers timeouts and DNS failures
	if _, ok := err.(net.Error); ok {
		return true
	}
	// Check wrapped errors
	if urlErr, ok := err.(*url.Error); ok {
		return isNetworkError(urlErr.Err)
	}
	return false
}

const maxRetries = 3

// Do performs an authenticated API request and returns the raw body.
// It automatically retries on 429 with exponential backoff and wraps
// network errors with user-friendly messages.
func (c *Client) Do(method, path string, params *RequestParams) ([]byte, error) {
	fullURL := c.buildURL(path, params)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(method, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		// Auth — X-API-Key header per LAEVITAS API convention
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", fmt.Sprintf("laevitas-cli/%s", version.Version))

		if c.Verbose {
			dump, _ := httputil.DumpRequestOut(req, false)
			fmt.Fprintf(os.Stderr, "\n--- REQUEST ---\n%s", string(dump))
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if isNetworkError(err) {
				return nil, &NetworkError{Err: err}
			}
			return nil, fmt.Errorf("request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}

		if c.Verbose {
			fmt.Fprintf(os.Stderr, "\n--- RESPONSE %d ---\n", resp.StatusCode)
			// Print headers
			for k, vals := range resp.Header {
				for _, v := range vals {
					fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
				}
			}
			// Print body (truncated at 2000 chars for readability)
			bodyStr := string(body)
			if len(bodyStr) > 2000 {
				bodyStr = bodyStr[:2000] + "\n... (truncated)"
			}
			fmt.Fprintf(os.Stderr, "\n%s\n", bodyStr)
		}

		if resp.StatusCode == http.StatusOK {
			return body, nil
		}

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
			Endpoint:   path,
		}

		// 401/403: auth error — no retry
		if apiErr.IsAuthError() {
			apiErr.Message = "API key invalid or expired. Run `laevitas config init` to update."
			return nil, apiErr
		}

		// 429: rate limited — retry with backoff
		if apiErr.IsRateLimit() && attempt < maxRetries {
			wait := retryDelay(resp, attempt)
			fmt.Fprintf(os.Stderr, "\033[33m⚠ Rate limited. Retrying in %s...\033[0m\n", wait.Round(time.Second))
			time.Sleep(wait)
			continue
		}

		return nil, apiErr
	}

	return nil, &APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "Rate limited. Max retries exceeded.",
		Endpoint:   path,
	}
}

// retryDelay calculates how long to wait before retrying a 429.
// Uses Retry-After header if present, otherwise exponential backoff.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	// Exponential backoff: 2s, 4s, 8s
	return time.Duration(1<<uint(attempt+1)) * time.Second
}

// APIResponse is the standard V2 API response wrapper.
type APIResponse struct {
	Data       json.RawMessage `json:"data"`
	Count      int             `json:"count,omitempty"`
	Meta       *ResponseMeta   `json:"meta,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// ResponseMeta contains pagination metadata.
type ResponseMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

// Get is a convenience wrapper for GET requests.
func (c *Client) Get(path string, params *RequestParams) ([]byte, error) {
	return c.Do(http.MethodGet, path, params)
}

// GetJSON performs a GET and unmarshals into the provided target.
func (c *Client) GetJSON(path string, params *RequestParams, target interface{}) error {
	body, err := c.Get(path, params)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}
