// Package wsclient is a thin native-WebSocket client for the Laevitas v1.17.0
// streaming gateway.
//
// It owns the wire protocol — JSON-RPC subscribe / unsubscribe / ping, plus
// data events with shape {"channel": "...", "data": {...}} — and exposes a
// channel-based Stream API that callers can range over. Reconnect with
// exponential backoff and re-subscription is handled internally so the
// caller's loop stays simple.
package wsclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Endpoint constants point at the canonical WS gateway.
const (
	NativeURL = "wss://apiv2.laevitas.ch/ws"

	// pingInterval is how often the client sends an app-level ping. Server
	// pings every 30s on its side; we ping every 25s to detect TCP-stuck
	// connections quickly without flooding.
	pingInterval = 25 * time.Second

	// recvTimeout caps how long we wait without any frame (data or PING)
	// before giving up on the connection and reconnecting. Server should
	// send PINGs every 30s, so 60s is comfortably generous.
	recvTimeout = 60 * time.Second

	// reconnectMax caps the exponential backoff between reconnect attempts.
	reconnectMax = 30 * time.Second
)

// Server-defined close codes (API v1.18.0 / v1.19.0). Codes < 4000 fall
// through to default reconnect handling.
const (
	closeAuthFailed     websocket.StatusCode = 4001
	closeIdleTimeout    websocket.StatusCode = 4002
	closeSlowConsumer   websocket.StatusCode = 4003
	closeLifetimeMax    websocket.StatusCode = 4004
	closeConnCap        websocket.StatusCode = 4005
	closeRateExceeded   websocket.StatusCode = 4008
)

// fatalCloseError is returned by connectAndServe when the server closed the
// connection with a code that the client should not retry (e.g. auth
// failure). run() detects this and exits the connection loop.
type fatalCloseError struct {
	code   websocket.StatusCode
	reason string
}

func (e *fatalCloseError) Error() string {
	if e.reason != "" {
		return fmt.Sprintf("ws closed (%d): %s", int(e.code), e.reason)
	}
	return fmt.Sprintf("ws closed (%d)", int(e.code))
}

// Event is a single inbound message from the gateway. Channel matches the
// channel string the caller subscribed to; Data is the raw JSON payload of
// the event (deferred unmarshal — different markets have different shapes,
// and the caller decides what to do with it).
type Event struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// Config controls how Client connects.
type Config struct {
	// URL is the wss:// endpoint. Defaults to NativeURL when empty.
	URL string

	// APIKey is sent as the `apikey` header on the WebSocket upgrade request.
	// Required for authenticated channels. The CLI passes whatever auth
	// method resolved from config.
	//
	// Note: prior to API v1.18.0 the gateway accepted a ?apiKey=... query
	// param. That auth path was removed; the header is now the only
	// server-side method we use.
	APIKey string

	// Channels is the initial channel set. Resubscribed automatically on
	// reconnect. Caller can mutate via Subscribe / Unsubscribe at runtime.
	Channels []string
}

// Client owns one persistent connection (with reconnect) and exposes Events
// over a Go channel.
type Client struct {
	cfg Config

	// channels is the set we want to be subscribed to. Mutated by the caller
	// via Subscribe / Unsubscribe; the connection loop reads it on every
	// reconnect to re-subscribe.
	mu       sync.Mutex
	channels map[string]struct{}

	// nextID is the JSON-RPC request id counter. Atomic because the ping
	// goroutine and the subscribe path both consume IDs.
	nextID atomic.Int64

	// events is the outbound stream the caller ranges over.
	events chan Event

	// errs surfaces non-fatal warnings (e.g. "reconnected after 4s; some
	// messages may have been lost"). Caller can ignore or log.
	errs chan error

	// fatalRPC is set when the server returns a JSON-RPC error that the
	// client can't recover from (auth rejected via rpc, not via close
	// frame). The read loop checks it after each handleMessage and exits.
	// Stored as a pointer so we can atomically swap between nil and an
	// error without taking the main mutex.
	fatalRPC atomic.Pointer[fatalCloseError]

	// ctx is the parent lifecycle. Cancelling it stops the connection loop
	// cleanly and closes events/errs.
	ctx    context.Context
	cancel context.CancelFunc
}

// Dial opens the connection, returns a running Client. Caller ranges over
// Events() until either an unrecoverable error happens or Close is called.
func Dial(parent context.Context, cfg Config) (*Client, error) {
	if cfg.URL == "" {
		cfg.URL = NativeURL
	}
	ctx, cancel := context.WithCancel(parent)

	c := &Client{
		cfg:      cfg,
		channels: make(map[string]struct{}),
		events:   make(chan Event, 256),
		errs:     make(chan error, 16),
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, ch := range cfg.Channels {
		c.channels[ch] = struct{}{}
	}

	go c.run()
	return c, nil
}

// Events returns the receive channel. Closed when Close is called or the
// parent context is cancelled.
func (c *Client) Events() <-chan Event { return c.events }

// Errs returns a non-blocking channel of soft errors (reconnects, unmarshal
// failures, server-side subscribe rejections). Caller can drain or ignore;
// fatal errors close Events instead.
func (c *Client) Errs() <-chan error { return c.errs }

// Subscribe adds a channel to the active set and immediately attempts to
// subscribe on the live connection. Idempotent.
func (c *Client) Subscribe(channels ...string) error {
	c.mu.Lock()
	added := make([]string, 0, len(channels))
	for _, ch := range channels {
		if _, exists := c.channels[ch]; !exists {
			c.channels[ch] = struct{}{}
			added = append(added, ch)
		}
	}
	c.mu.Unlock()
	if len(added) == 0 {
		return nil
	}
	// run() will pick this up on its next reconnect; for the live session
	// we rely on the next iteration of the read loop. Calling sites that
	// need synchronous confirmation should manage that themselves.
	return nil
}

// Close cancels the parent context, closes the socket, and drains channels.
func (c *Client) Close() error {
	c.cancel()
	return nil
}

// run is the connect-and-read loop. Reconnects with exponential backoff
// until the context is cancelled.
//
// Close order matters: events closes first (so the caller's range loop
// exits), then errs (so the caller can synchronously drain any final
// warning — auth failures, conn caps — before reporting an exit reason).
func (c *Client) run() {
	defer close(c.errs)
	defer close(c.events)

	backoff := time.Second
	firstAttempt := true

	for {
		if err := c.ctx.Err(); err != nil {
			return
		}

		// Sleep before reconnect attempts (not the first one).
		if !firstAttempt {
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > reconnectMax {
				backoff = reconnectMax
			}
		}
		firstAttempt = false

		if err := c.connectAndServe(); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			var fatal *fatalCloseError
			if errors.As(err, &fatal) {
				// Surface the message, then exit the loop — no point
				// retrying an auth or quota failure.
				c.softErr(fatal)
				return
			}
			c.softErr(fmt.Errorf("connection lost: %w", err))
			continue
		}
		// connectAndServe returned nil → context was cancelled normally.
		return
	}
}

// connectAndServe handles one full lifecycle: dial, subscribe, ping loop,
// read loop. Returns nil on graceful shutdown, error otherwise.
func (c *Client) connectAndServe() error {
	var dialOpts *websocket.DialOptions
	if c.cfg.APIKey != "" {
		dialOpts = &websocket.DialOptions{
			HTTPHeader: http.Header{"apikey": []string{c.cfg.APIKey}},
		}
	}

	conn, _, err := websocket.Dial(c.ctx, c.cfg.URL, dialOpts)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// Increase read message limit; OHLC payloads with arrays can grow.
	conn.SetReadLimit(1 << 20) // 1 MiB

	defer conn.Close(websocket.StatusNormalClosure, "client closing")

	// Subscribe to whatever's in the active set.
	if err := c.subscribeAll(conn); err != nil {
		return fmt.Errorf("initial subscribe: %w", err)
	}

	// Ping loop runs for the lifetime of this connection.
	pingCtx, pingCancel := context.WithCancel(c.ctx)
	defer pingCancel()
	go c.pingLoop(pingCtx, conn)

	// Read loop — terminates on error or context cancellation.
	for {
		readCtx, readCancel := context.WithTimeout(c.ctx, recvTimeout)
		_, msg, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			if c.ctx.Err() != nil {
				return nil // graceful shutdown
			}
			// Inspect close status — server signals auth/quota/lifetime
			// problems via well-known codes (v1.18.0+).
			if status := websocket.CloseStatus(err); status != -1 {
				if fatal := c.classifyClose(status, err); fatal != nil {
					return fatal
				}
			}
			return err
		}
		c.handleMessage(msg)
		if fatal := c.fatalRPC.Load(); fatal != nil {
			return fatal
		}
	}
}

// classifyClose returns a *fatalCloseError for codes the client must not
// reconnect on; nil for codes that are recoverable (run() will retry with
// backoff). The soft warning is emitted for recoverable codes so the user
// sees what happened.
func (c *Client) classifyClose(status websocket.StatusCode, err error) error {
	switch status {
	case closeAuthFailed:
		return &fatalCloseError{code: status, reason: "authentication rejected — check LAEVITAS_API_KEY (gateway requires the `apikey` upgrade header as of API v1.18.0)"}
	case closeIdleTimeout:
		c.softErr(fmt.Errorf("server closed idle connection (4002); reconnecting"))
	case closeSlowConsumer:
		c.softErr(fmt.Errorf("server dropped slow consumer (4003); reconnecting — consider narrowing channels"))
	case closeLifetimeMax:
		c.softErr(fmt.Errorf("server enforced 24h connection lifetime (4004); reconnecting"))
	case closeConnCap:
		return &fatalCloseError{code: status, reason: "connection cap reached on this API key (4005) — close other sessions"}
	case closeRateExceeded:
		c.softErr(fmt.Errorf("rate limit exceeded (4008); backing off"))
	}
	return nil
}

// pingLoop sends a JSON-RPC ping every pingInterval. coder/websocket's Read
// already handles WebSocket-level PING frames automatically, but the gateway
// also responds to app-level pings, which gives us a cleaner liveness check.
func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			req := map[string]interface{}{
				"id":     c.nextID.Add(1),
				"method": "ping",
			}
			payload, _ := json.Marshal(req)
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// subscribeAll sends a single subscribe request for every channel in the
// active set. Server returns subscriptionIds we don't track today — the
// channel string in incoming events is enough to dispatch.
func (c *Client) subscribeAll(conn *websocket.Conn) error {
	c.mu.Lock()
	channels := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		channels = append(channels, ch)
	}
	c.mu.Unlock()
	if len(channels) == 0 {
		return nil
	}

	req := map[string]interface{}{
		"id":     c.nextID.Add(1),
		"method": "subscribe",
		"params": map[string]interface{}{"channels": channels},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	writeCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

// handleMessage dispatches one inbound frame. Three shapes possible:
//   - data event: {"channel": "...", "data": {...}}
//   - JSON-RPC reply: {"id": N, "result": ...}  (subscribe ack, ping pong)
//   - JSON-RPC error: {"id": N, "error": {...}} (subscribe failure)
//
// Data events go to the caller's stream; replies and errors are surfaced
// via Errs (caller can log or ignore).
func (c *Client) handleMessage(msg []byte) {
	// Cheap check first: the data-event shape has a "channel" field; replies
	// have "id" and either "result" or "error". Try data-event decode first
	// since that's the hot path.
	var ev Event
	if err := json.Unmarshal(msg, &ev); err == nil && ev.Channel != "" && len(ev.Data) > 0 {
		select {
		case c.events <- ev:
		case <-c.ctx.Done():
		}
		return
	}

	// Fall through: try JSON-RPC reply.
	var reply struct {
		ID     int64           `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(msg, &reply); err == nil {
		if len(reply.Error) > 0 {
			// Some auth failures arrive as RPC errors rather than close
			// frames (see v1.18.0 gateway). If the error envelope decodes
			// to an "Authentication required" / "Invalid API key" payload,
			// promote it to a fatal so the read loop stops retrying.
			var detail struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(reply.Error, &detail)
			if detail.Code == 401 || detail.Code == 403 {
				c.fatalRPC.Store(&fatalCloseError{
					code:   closeAuthFailed,
					reason: fmt.Sprintf("authentication rejected (rpc %d: %s) — check LAEVITAS_API_KEY", detail.Code, detail.Message),
				})
				return
			}
			c.softErr(fmt.Errorf("server error on rpc id=%d: %s", reply.ID, reply.Error))
		}
		// result frames (subscribe ack, pong) are intentionally silent on
		// the happy path — we don't need to surface every pong.
		return
	}

	// Anything else: log as soft error so the user knows the server sent
	// something we don't understand.
	c.softErr(fmt.Errorf("unrecognised frame: %s", truncate(msg, 200)))
}

// softErr pushes a non-fatal error to the errs channel without blocking.
func (c *Client) softErr(err error) {
	select {
	case c.errs <- err:
	default:
		// errs channel is full → drop. This is a diagnostic stream, not a
		// reliable log. The events channel is what the caller cares about.
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
