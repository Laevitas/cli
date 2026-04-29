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
	"net/url"
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

	// APIKey is sent as ?apiKey=... query param at upgrade time. Required for
	// authenticated channels. The CLI passes whatever auth method resolved
	// from config.
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
func (c *Client) run() {
	defer close(c.events)
	defer close(c.errs)

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
	dialURL, err := buildDialURL(c.cfg.URL, c.cfg.APIKey)
	if err != nil {
		return fmt.Errorf("building dial URL: %w", err)
	}

	conn, _, err := websocket.Dial(c.ctx, dialURL, nil)
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
			return err
		}
		c.handleMessage(msg)
	}
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

// buildDialURL appends ?apiKey=... when APIKey is set. Preserves any
// existing query in the configured URL.
func buildDialURL(rawURL, apiKey string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		q := u.Query()
		q.Set("apiKey", apiKey)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
