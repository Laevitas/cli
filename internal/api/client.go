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
	"github.com/laevitas/cli/internal/x402"
)

// Payment method constants for tracking how each request was authenticated.
const (
	PaymentMethodAPIKey  = "api-key"
	PaymentMethodCredit  = "credit"
	PaymentMethodOnChain = "on-chain"
)

// RequestMeta holds metadata from the last API request.
type RequestMeta struct {
	Duration      time.Duration
	PaymentMethod string // "api-key", "credit", "on-chain"
	Credits       string // remaining credits (x402)
	Retries       int    // number of 429 retries before success
	ResponseSize  int    // response body size in bytes
}

// Client is the LAEVITAS API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	Verbose    bool

	// x402 payment support
	paymentClient *x402.PaymentClient
	creditToken   string // cached JWT credit token

	// LastMeta contains metadata from the most recent request.
	LastMeta RequestMeta
}

// HasWallet returns true if x402 wallet payment is configured.
func (c *Client) HasWallet() bool {
	return c.paymentClient != nil
}

// NewClient creates a new API client from config.
func NewClient(cfg *config.Config) *Client {
	c := &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Initialize x402 payment client if wallet key is configured
	if cfg.WalletKey != "" {
		pc, err := x402.NewPaymentClient(cfg.WalletKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[33m⚠ Invalid wallet key: %s\033[0m\n", err)
		} else {
			c.paymentClient = pc
			c.creditToken = config.LoadCreditToken()
		}
	}

	return c
}

// WalletAddress returns the x402 wallet address, or empty if not configured.
func (c *Client) WalletAddress() string {
	if c.paymentClient == nil {
		return ""
	}
	return c.paymentClient.Address()
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
	c.LastMeta = RequestMeta{} // reset for each call
	startTime := time.Now()

	usedCredit := false

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(method, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		// Auth — header name is "apiKey" per LAEVITAS API convention
		if c.apiKey != "" {
			req.Header.Set("apiKey", c.apiKey)
		}
		// Send cached x402 credit token if available (and no API key)
		if c.apiKey == "" && c.creditToken != "" {
			req.Header.Set("X-Credit-Token", c.creditToken)
			usedCredit = true
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", fmt.Sprintf("laevitas-cli/%s (+https://github.com/laevitas/cli)", version.Version))

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

		// Cache credit token and credits remaining from any response
		c.extractCreditHeaders(resp)

		if resp.StatusCode == http.StatusOK {
			// Track request metadata
			c.LastMeta.Duration = time.Since(startTime)
			c.LastMeta.ResponseSize = len(body)
			c.LastMeta.Retries = attempt
			if c.apiKey != "" {
				c.LastMeta.PaymentMethod = PaymentMethodAPIKey
			} else if usedCredit {
				c.LastMeta.PaymentMethod = PaymentMethodCredit
			}
			return body, nil
		}

		// 402: Payment Required — try x402 payment
		if resp.StatusCode == http.StatusPaymentRequired {
			result, err := c.handlePaymentRequired(method, fullURL, resp, body, path)
			c.LastMeta.Duration = time.Since(startTime)
			return result, err
		}

		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
			Endpoint:   path,
		}

		// 401/403: auth error
		if apiErr.IsAuthError() {
			// If wallet is configured (no API key), treat 401 as 402 — trigger x402 payment
			if c.apiKey == "" && c.paymentClient != nil {
				result, err := c.handlePaymentRequired(method, fullURL, resp, body, path)
				c.LastMeta.Duration = time.Since(startTime)
				return result, err
			}
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

// extractCreditHeaders caches x402 credit token and remaining credits from response.
func (c *Client) extractCreditHeaders(resp *http.Response) {
	if token := resp.Header.Get("X-Credit-Token"); token != "" {
		c.creditToken = token
		_ = config.SaveCreditToken(token)
	}
	if remaining := resp.Header.Get("X-Credits-Remaining"); remaining != "" {
		c.LastMeta.Credits = remaining
	}
}

// handlePaymentRequired processes a 402 response by signing an x402 payment and retrying.
func (c *Client) handlePaymentRequired(method, fullURL string, resp *http.Response, body []byte, path string) ([]byte, error) {
	// If we sent a credit token that was rejected, clear it
	if c.creditToken != "" {
		c.creditToken = ""
		config.ClearCreditToken()
	}

	// No wallet configured — can't pay
	if c.paymentClient == nil {
		return nil, &APIError{
			StatusCode: http.StatusPaymentRequired,
			Message:    "Payment required. Set a wallet key with `laevitas config set wallet_key <key>` or use an API key.",
			Endpoint:   path,
		}
	}

	walletAddr := c.paymentClient.Address()

	// Sign payment using x402 protocol
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "\n--- x402: Signing payment with wallet %s ---\n", walletAddr)
	}

	paymentHeaders, err := c.paymentClient.HandlePaymentRequired(resp, body)
	if err != nil {
		// Provide specific guidance based on the failure
		msg := fmt.Sprintf("x402 payment signing failed: %s\n", err)
		msg += fmt.Sprintf("  Wallet: %s\n", walletAddr)
		msg += "  Ensure your wallet has USDC on Base chain.\n"
		msg += "  If this persists, try `laevitas config set wallet_key <key>` with a valid EVM private key (0x-prefixed)."
		return nil, &APIError{
			StatusCode: http.StatusPaymentRequired,
			Message:    msg,
			Endpoint:   path,
		}
	}

	// Retry request with payment signature
	retryReq, err := http.NewRequest(method, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating retry request: %w", err)
	}

	if c.apiKey != "" {
		retryReq.Header.Set("apiKey", c.apiKey)
	}
	retryReq.Header.Set("Accept", "application/json")
	retryReq.Header.Set("User-Agent", fmt.Sprintf("laevitas-cli/%s (+https://github.com/laevitas/cli)", version.Version))

	// Add payment signature headers
	for k, v := range paymentHeaders {
		retryReq.Header.Set(k, v)
	}

	if c.Verbose {
		dump, _ := httputil.DumpRequestOut(retryReq, false)
		fmt.Fprintf(os.Stderr, "\n--- x402 RETRY REQUEST ---\n%s", string(dump))
	}

	retryResp, err := c.httpClient.Do(retryReq)
	if err != nil {
		if isNetworkError(err) {
			return nil, &NetworkError{Err: err}
		}
		return nil, fmt.Errorf("x402 retry failed: %w", err)
	}

	retryBody, err := io.ReadAll(retryResp.Body)
	retryResp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading x402 retry response: %w", err)
	}

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "\n--- x402 RETRY RESPONSE %d ---\n", retryResp.StatusCode)
		for k, vals := range retryResp.Header {
			for _, v := range vals {
				fmt.Fprintf(os.Stderr, "%s: %s\n", k, v)
			}
		}
	}

	// Cache credit token from retry response
	c.extractCreditHeaders(retryResp)

	if retryResp.StatusCode == http.StatusOK {
		c.LastMeta.PaymentMethod = PaymentMethodOnChain
		c.LastMeta.ResponseSize = len(retryBody)
		return retryBody, nil
	}

	// Payment was signed but server rejected it — build a helpful error
	msg := c.buildPaymentErrorMessage(retryResp.StatusCode, retryBody, walletAddr)
	return nil, &APIError{
		StatusCode: retryResp.StatusCode,
		Message:    msg,
		Endpoint:   path,
	}
}

// buildPaymentErrorMessage creates a user-friendly error for rejected x402 payments.
func (c *Client) buildPaymentErrorMessage(statusCode int, body []byte, walletAddr string) string {
	// Try to extract error details from response body
	var parsed struct {
		Message      string `json:"message"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
		ErrorReason  string `json:"errorReason"`
	}
	serverMsg := ""
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.ErrorMessage != "" {
			serverMsg = parsed.ErrorMessage
		} else if parsed.ErrorReason != "" {
			serverMsg = parsed.ErrorReason
		} else if parsed.Message != "" {
			serverMsg = parsed.Message
		} else if parsed.Error != "" {
			serverMsg = parsed.Error
		}
	}
	if serverMsg == "" && len(body) > 2 {
		// Use raw body if it's not just "{}"
		serverMsg = string(body)
	}

	switch statusCode {
	case http.StatusPaymentRequired:
		msg := "Payment rejected by server."
		if serverMsg != "" {
			msg = fmt.Sprintf("Payment rejected: %s", serverMsg)
		}
		msg += fmt.Sprintf("\n  Wallet:  %s", walletAddr)
		msg += "\n  Check:   Does this wallet have USDC on Base chain?"
		msg += "\n  Verify:  Run `laevitas config show` to confirm wallet address"
		return msg
	case http.StatusForbidden:
		return fmt.Sprintf("Payment forbidden: %s\n  The server rejected the payment signature.", serverMsg)
	default:
		if serverMsg != "" {
			return fmt.Sprintf("Payment failed (HTTP %d): %s", statusCode, serverMsg)
		}
		return fmt.Sprintf("Payment failed (HTTP %d). Run with --verbose for details.", statusCode)
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
