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

	// subRetryMax caps how many times the client retries a per-channel
	// subscribe RPC before giving up on that channel for the lifetime of
	// the current connection. Reset on reconnect. Bounded so a permanently-
	// bad channel (typo, server-side disabled) doesn't pin the retry loop;
	// generous enough to ride out a flaky first-attempt where the server
	// briefly rejects subscribes during auth handshake completion.
	subRetryMax = 3

	// subRetryDelay is the wait between per-channel subscribe retries.
	// Short — the goal is to ride out transient server-side glitches, not
	// to back off through a sustained outage (run() handles those via full
	// reconnect with exponential backoff).
	subRetryDelay = 2 * time.Second
)

// Server-defined close codes (API v1.18.0 / v1.19.0). Codes < 4000 fall
// through to default reconnect handling.
const (
	closeAuthFailed   websocket.StatusCode = 4001
	closeIdleTimeout  websocket.StatusCode = 4002
	closeSlowConsumer websocket.StatusCode = 4003
	closeLifetimeMax  websocket.StatusCode = 4004
	closeConnCap      websocket.StatusCode = 4005
	closeRateExceeded websocket.StatusCode = 4008
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

// subState tracks the lifecycle of a single channel's subscribe across
// the current connection. Reset to subPending on every reconnect (the
// server has no memory of our prior subs after a TCP teardown).
type subState int

const (
	// subPending: registered with this client (via Subscribe or initial
	// Channels) but no subscribe RPC has been sent on the current
	// connection yet, OR a prior attempt failed and we're between
	// retries.
	subPending subState = iota
	// subInFlight: subscribe RPC has been written; awaiting ack/error.
	subInFlight
	// subAcked: server returned `result` for our subscribe — the
	// channel is live and we should be receiving events on it.
	subAcked
	// subFailed: retry budget exhausted on the current connection; we
	// stop retrying until the next reconnect (which resets all states).
	subFailed
	// subUnsubAfterAck: caller invoked Unsubscribe on a channel whose
	// subscribe RPC was in flight. We can't unsubscribe immediately —
	// the server hasn't issued a subscriptionId yet — so we mark the
	// entry to be torn down as soon as the ack arrives. handleSubscribeAck
	// looks for this state and fires the unsubscribe RPC using the
	// just-received subscriptionId, then drops the entry from the map.
	// Without this state, an Unsubscribe-during-subscribe-in-flight
	// would leave the server holding a phantom subscription for the
	// remainder of the connection's lifetime.
	subUnsubAfterAck
)

// subEntry is the per-channel record threaded through the subscribe
// retry loop. Bookkeeping that doesn't escape the wsclient package.
type subEntry struct {
	state          subState
	attempts       int       // how many RPCs sent on this connection
	rpcID          int64     // current in-flight RPC id (when state == subInFlight)
	nextTry        time.Time // earliest time to retry when state == subPending after a failure
	subscriptionID string    // server-issued id from the subscribe ack; required to unsubscribe
}

// Client owns one persistent connection (with reconnect) and exposes Events
// over a Go channel.
type Client struct {
	cfg Config

	// channels is the set we want to be subscribed to, with per-channel
	// subscribe lifecycle state. Mutated by the caller via Subscribe /
	// Unsubscribe and by the read/retry loops; everything goes through mu.
	mu       sync.Mutex
	channels map[string]*subEntry

	// activeConn holds the live websocket connection during the lifetime
	// of one connectAndServe call, nil otherwise. Set under mu at the top
	// of connectAndServe and cleared via defer when that function returns.
	// Callers (Unsubscribe) read it under mu to send out-of-band RPCs to
	// the live socket; the underlying coder/websocket Conn is documented
	// safe for concurrent Write calls (Read/Reader is the only exclusion).
	activeConn *websocket.Conn

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
		channels: make(map[string]*subEntry),
		events:   make(chan Event, 256),
		errs:     make(chan error, 16),
		ctx:      ctx,
		cancel:   cancel,
	}
	for _, ch := range cfg.Channels {
		c.channels[ch] = &subEntry{state: subPending}
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

// Subscribe adds a channel to the active set. Idempotent. The channel
// will be picked up by the per-channel subscribe loop on its next tick
// (or on the next reconnect if the connection is currently down).
//
// Resubscribe-before-ack handling: if a channel exists in the
// subUnsubAfterAck tombstone state (caller previously called
// Unsubscribe while the subscribe RPC was still in flight), Subscribe
// flips it back to subInFlight to preserve the in-flight RPC. The ack
// handler will then treat it as a normal subscribe ack rather than
// firing the tombstone unsubscribe path. Without this, sequence
// "subscribe→unsubscribe→subscribe" all before the first ack would
// silently drop the renewed intent.
func (c *Client) Subscribe(channels ...string) error {
	c.mu.Lock()
	for _, ch := range channels {
		entry, exists := c.channels[ch]
		if !exists {
			c.channels[ch] = &subEntry{state: subPending}
			continue
		}
		if entry.state == subUnsubAfterAck {
			// Restore: caller changed their mind about unsubscribing.
			// rpcID is preserved (the same subscribe RPC is still
			// in flight server-side), so the ack handler matches it
			// and lands in the normal subAcked path.
			entry.state = subInFlight
		}
	}
	c.mu.Unlock()
	return nil
}

// Unsubscribe removes channels from the active set. Behaviour by
// per-channel state at the time of the call:
//
//   - subAcked (subscriptionId known): drop the local entry, send a
//     JSON-RPC unsubscribe to the gateway using the recorded id.
//   - subInFlight (subscribe RPC pending, no id yet): keep the entry
//     and flip its state to subUnsubAfterAck. The ack handler picks
//     this up and fires the unsubscribe RPC as soon as the id arrives.
//     Without this, an Unsubscribe-during-in-flight-subscribe would
//     leave the server holding a phantom subscription for the rest of
//     the connection.
//   - subUnsubAfterAck (already tombstoned by a prior Unsubscribe):
//     no-op. The tombstone must persist until the ack/error/reconnect
//     resolves it, so a second Unsubscribe deliberately does NOT
//     delete it — deleting would resurrect the original leak.
//   - subPending / subFailed: drop the local entry. Nothing to tell
//     the gateway — those channels were never acked server-side.
//
// Idempotent: unsubscribing a channel that was never subscribed is a
// no-op. Repeated Unsubscribe of an in-flight subscribe converges on
// the tombstone state (first call sets it, subsequent calls preserve
// it). Returns the first write error encountered when sending
// unsubscribe RPCs; local state is always updated regardless, so a
// transient write failure still removes the channels from the client's
// intent set.
func (c *Client) Unsubscribe(channels ...string) error {
	type pending struct {
		channel        string
		subscriptionID string
	}
	c.mu.Lock()
	toUnsub := make([]pending, 0, len(channels))
	for _, ch := range channels {
		entry, ok := c.channels[ch]
		if !ok {
			continue
		}
		switch entry.state {
		case subAcked:
			if entry.subscriptionID != "" {
				toUnsub = append(toUnsub, pending{channel: ch, subscriptionID: entry.subscriptionID})
			}
			delete(c.channels, ch)
		case subInFlight:
			// Tombstone: keep the entry so handleSubscribeAck can match
			// the imminent ack against it and fire unsubscribe with the
			// server-issued id. Caller's intent (this channel is gone)
			// is honoured by the read loop: data events for an entry in
			// subUnsubAfterAck are still delivered until the unsubscribe
			// completes (the gateway can keep emitting until it processes
			// our unsubscribe). That's acceptable; the alternative
			// (dropping the entry now) leaks server-side subscription
			// for the connection's lifetime.
			entry.state = subUnsubAfterAck
		case subUnsubAfterAck:
			// Already tombstoned by a prior Unsubscribe. Keep it — the
			// ack handler still needs the entry to fire the unsubscribe
			// RPC when the in-flight subscribe ack arrives. Deleting it
			// here would resurrect the original leak (subscribe in
			// flight, server happily creates the subscription, we have
			// no entry to match the ack against, nothing fires the
			// unsubscribe). Idempotent: second/Nth Unsubscribe is a
			// no-op against a tombstoned entry.
			continue
		default:
			// subPending, subFailed — never acked server-side. Local-only drop.
			delete(c.channels, ch)
		}
	}
	conn := c.activeConn
	c.mu.Unlock()

	if conn == nil || len(toUnsub) == 0 {
		// Either no live connection or nothing to tell the gateway about;
		// local state is already updated.
		return nil
	}

	// Per protocol spec (apiv2.laevitas.ch/websocket/), unsubscribe takes
	// one subscriptionId per call. Send one RPC per channel rather than
	// trying to bundle — bundling is not documented for unsubscribe and
	// the per-RPC cost is negligible compared to a typical reconnect.
	var firstErr error
	for _, p := range toUnsub {
		req := map[string]interface{}{
			"id":     c.nextID.Add(1),
			"method": "unsubscribe",
			"params": map[string]interface{}{"subscriptionId": p.subscriptionID},
		}
		payload, err := json.Marshal(req)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		writeCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, payload)
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

	// Atomically install the new conn AND reset the per-channel bookkeeping
	// (clear stale subscriptionIDs, drop tombstones, reset retry counters)
	// under a single mutex acquisition. If we published activeConn first
	// and reset after, a concurrent Unsubscribe in the window between
	// could read the new activeConn but a stale subscriptionID from the
	// previous connection — and address the wrong subscription on the
	// fresh gateway. Doing both under one lock closes that race.
	c.installConnAndReset(conn)
	defer func() {
		c.mu.Lock()
		c.activeConn = nil
		c.mu.Unlock()
	}()

	// First subscribe pass before starting the read/retry loops. Errors
	// here are write errors (socket already broken) — surface as a hard
	// connection failure so run() reconnects with backoff. Per-channel
	// RPC errors aren't returned here; they come back asynchronously via
	// reply frames and are handled by the retry loop.
	if err := c.subscribePending(conn); err != nil {
		return fmt.Errorf("initial subscribe: %w", err)
	}

	// Ping + retry loops run for the lifetime of this connection.
	pingCtx, pingCancel := context.WithCancel(c.ctx)
	defer pingCancel()
	go c.pingLoop(pingCtx, conn)
	go c.subscribeRetryLoop(pingCtx, conn)

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

// installConnAndReset publishes the new conn and resets per-channel
// bookkeeping atomically under one mutex acquisition. Both operations
// must happen together: if we published the conn first and any
// concurrent Unsubscribe ran in the window before reset, the call
// could read the new activeConn paired with a stale subscriptionID
// from the previous connection — and ship the wrong id to the fresh
// gateway. Single-lock helper closes that window.
//
// Tombstoned entries (subUnsubAfterAck) are deleted outright rather
// than reset; see resetSubsForReconnectLocked for the rationale.
func (c *Client) installConnAndReset(conn *websocket.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.activeConn = conn
	c.resetSubsForReconnectLocked()
}

// resetSubsForReconnect flips every entry back to subPending and clears
// retry counters. Called once at the top of each connection so the
// per-channel subscribe state reflects "nothing subscribed yet on this
// socket" — server-side state is gone after a TCP teardown, including
// any subscriptionIds we'd recorded under the previous connection.
// Carrying them over would be unsafe: a fresh subscribe gets a fresh
// id, and an Unsubscribe call between disconnect and re-ack might
// otherwise send the previous connection's id to the new gateway,
// which would either no-op or hit a different subscription.
//
// Tombstoned entries (subUnsubAfterAck) are deleted outright on
// reconnect rather than reset to subPending. They were created by an
// Unsubscribe call against an in-flight subscribe; the connection
// teardown already wiped the server-side subscription, so there's
// nothing to unsubscribe and the caller's intent (channel gone) wins.
// Resurrecting them as subPending would re-subscribe a channel the
// caller explicitly removed.
//
// The exported wrapper acquires c.mu; resetSubsForReconnectLocked is
// the inner version for callers (installConnAndReset) that already
// hold the lock.
func (c *Client) resetSubsForReconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetSubsForReconnectLocked()
}

func (c *Client) resetSubsForReconnectLocked() {
	for ch, e := range c.channels {
		if e.state == subUnsubAfterAck {
			delete(c.channels, ch)
			continue
		}
		e.state = subPending
		e.attempts = 0
		e.rpcID = 0
		e.nextTry = time.Time{}
		e.subscriptionID = "" // gateway will issue a fresh one on this connection
	}
}

// subscribePending issues one bundled subscribe RPC carrying every
// channel currently in subPending state whose nextTry has elapsed. All
// included channels share the same rpcID (the bundle's id); the reply
// (handled in handleMessage) flips them to subAcked or schedules
// another retry.
//
// Bundled rather than per-channel because the gateway protocol
// (https://apiv2.laevitas.ch/websocket/) accepts an array of channels
// per subscribe and that's the form the rate limiter (20 inbound
// msgs/sec) is sized for. The success reply echoes back a `channels`
// array — anything we asked for that's missing from that echo gets
// retried; a full RPC error puts the whole bundle back into pending.
//
// Returns a write error (socket dead) if conn.Write fails. Per-channel
// rejections come back asynchronously and are not returned here.
func (c *Client) subscribePending(conn *websocket.Conn) error {
	now := time.Now()
	c.mu.Lock()
	due := make([]string, 0)
	dueEntries := make([]*subEntry, 0)
	for ch, e := range c.channels {
		if e.state != subPending {
			continue
		}
		if !e.nextTry.IsZero() && e.nextTry.After(now) {
			continue
		}
		due = append(due, ch)
		dueEntries = append(dueEntries, e)
	}
	if len(due) == 0 {
		c.mu.Unlock()
		return nil
	}
	id := c.nextID.Add(1)
	for _, e := range dueEntries {
		e.state = subInFlight
		e.attempts++
		e.rpcID = id
		// Clear any stale subscriptionId from a prior failed attempt or
		// an earlier connection that resetSubsForReconnect missed (e.g.
		// the entry was added mid-reconnect). Each fresh subscribe RPC
		// gets a fresh id from the gateway; keeping a stale one risks
		// addressing the wrong subscription on a later Unsubscribe.
		e.subscriptionID = ""
	}
	c.mu.Unlock()

	req := map[string]interface{}{
		"id":     id,
		"method": "subscribe",
		"params": map[string]interface{}{"channels": due},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		// Marshal failure is unexpected (we built the map ourselves).
		// Roll all entries back so they get retried on the next tick.
		c.mu.Lock()
		for _, e := range dueEntries {
			e.state = subPending
			e.rpcID = 0
			e.nextTry = now.Add(subRetryDelay)
		}
		c.mu.Unlock()
		return nil
	}
	writeCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	cancel()
	if err != nil {
		// Write failure means the socket is dead — bail out so the
		// caller can reconnect. Don't bother updating per-entry state
		// here; resetSubsForReconnect wipes it on next attempt.
		return err
	}
	return nil
}

// subscribeRetryLoop ticks every subRetryDelay and re-issues subscribes
// for any entries that bounced back into subPending after an RPC error.
// Lifetime tied to the connection (ctx is the same pingCtx so it dies
// with the read loop).
func (c *Client) subscribeRetryLoop(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(subRetryDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.subscribePending(conn); err != nil {
				// Write failure → connection is going down anyway; the
				// read loop will return an error and run() will reconnect.
				return
			}
		}
	}
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
			// Map the RPC id back to the channel that initiated it. If
			// the id matches a subscribe in flight, schedule a retry
			// (or mark failed if the budget's exhausted) instead of
			// silently swallowing the error — the original "flaky first
			// subscribe" symptom was the bundle subscribe getting one
			// rejection that took down the whole session's data flow.
			c.handleSubscribeError(reply.ID, reply.Error)
			return
		}
		// result frames: most are subscribe acks. Look up the id, read
		// the echoed `channels` array (which the gateway returns parallel
		// to the subscriptionIds — see protocol spec) and flip those
		// entries to subAcked. Any subInFlight entries that shared the
		// rpcID but were missing from the echo go back to subPending —
		// the gateway rejected them silently and the retry loop should
		// pick them up. Pongs come through here too; their id won't
		// match any pending subscribe so the lookup no-ops.
		c.handleSubscribeAck(reply.ID, reply.Result)
		return
	}

	// Anything else: log as soft error so the user knows the server sent
	// something we don't understand.
	c.softErr(fmt.Errorf("unrecognised frame: %s", truncate(msg, 200)))
}

// handleSubscribeAck reconciles a subscribe `result` frame against the
// channels we asked for under that rpc id. The gateway echoes back a
// `channels` array (parallel to subscriptionIds) listing the channels
// it actually accepted — anything we asked for that's missing from the
// echo gets re-armed for retry, anything present flips to subAcked.
//
// Pongs and other unmatched-id replies fall through silently (no entry
// has rpcID == id, so the loop touches nothing).
func (c *Client) handleSubscribeAck(id int64, result json.RawMessage) {
	if id == 0 {
		return
	}
	// Decode the result payload. Failure to decode (e.g. a non-subscribe
	// reply that happens to share the id of a finished subscribe — which
	// shouldn't happen but we don't crash on it) is treated as "all
	// in-flight under this id are acked", which is the existing behavior.
	//
	// The gateway returns subscriptionIds[] parallel to channels[] in the
	// success result — we record subscriptionId per channel so a future
	// Unsubscribe call can address the server-side subscription. Without
	// this we'd only be able to drop the channel from our local map and
	// hope the server eventually reaps it.
	accepted := map[string]string{} // channel → subscriptionId (may be empty)
	if len(result) > 0 {
		var resBody struct {
			Channels        []string `json:"channels"`
			SubscriptionIDs []string `json:"subscriptionIds"`
		}
		if err := json.Unmarshal(result, &resBody); err == nil {
			for i, ch := range resBody.Channels {
				subID := ""
				if i < len(resBody.SubscriptionIDs) {
					subID = resBody.SubscriptionIDs[i]
				}
				accepted[ch] = subID
			}
		}
	}

	now := time.Now()
	// Tombstoned channels (Unsubscribe called while subscribe was in
	// flight) need an unsubscribe RPC fired now that we have their
	// subscriptionId. We collect them under the lock and fire the
	// writes after unlock — conn.Write doesn't need the mutex and
	// holding it during a network op would block other RPCs.
	type tombstoneRPC struct {
		channel        string
		subscriptionID string
	}
	tombstones := []tombstoneRPC{}

	c.mu.Lock()
	for ch, e := range c.channels {
		if e.rpcID != id {
			continue
		}
		switch e.state {
		case subInFlight:
			if subID, ok := accepted[ch]; ok || len(accepted) == 0 {
				// Either the gateway echoed this channel back (full ack)
				// or we couldn't parse the echo and fall back to optimistic
				// "id matched, treat as acked" — same as the old behaviour.
				e.state = subAcked
				e.rpcID = 0
				if subID != "" {
					e.subscriptionID = subID
				}
				continue
			}
			// Asked for, but missing from the echo → re-arm for retry.
			// Keeps attempts incrementing so the retry budget still bounds
			// permanently-bad channels.
			if e.attempts >= subRetryMax {
				e.state = subFailed
				e.rpcID = 0
				continue
			}
			e.state = subPending
			e.rpcID = 0
			e.nextTry = now.Add(subRetryDelay)
		case subUnsubAfterAck:
			// Caller invoked Unsubscribe while the subscribe was in flight.
			// Now that the ack arrived, fire the unsubscribe with the
			// just-received subscriptionId and drop the entry. If the
			// gateway didn't echo this channel (server-side rejection)
			// there's nothing to unsubscribe — just drop locally.
			if subID, ok := accepted[ch]; ok && subID != "" {
				tombstones = append(tombstones, tombstoneRPC{channel: ch, subscriptionID: subID})
			}
			delete(c.channels, ch)
		}
	}
	conn := c.activeConn
	c.mu.Unlock()

	// Fire tombstone unsubscribes outside the lock. Errors here are
	// non-fatal — the local intent is already removed; if the gateway
	// missed our unsubscribe it'll be reset on next reconnect anyway.
	if conn != nil {
		for _, t := range tombstones {
			req := map[string]interface{}{
				"id":     c.nextID.Add(1),
				"method": "unsubscribe",
				"params": map[string]interface{}{"subscriptionId": t.subscriptionID},
			}
			payload, err := json.Marshal(req)
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			_ = conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
		}
	}
}

// handleSubscribeError reacts to an RPC error frame for a bundled
// subscribe. The gateway returns a single error per request rather than
// per-channel errors, so we roll every channel that shared the rpc id
// back into subPending (or terminally subFailed if the per-channel
// retry budget is exhausted). One soft-error is emitted per affected
// channel so the user sees which channels were impacted.
//
// Errors with no matching id are surfaced as a generic soft error
// (could be a stale subscribe whose ack we already saw, or an error
// for an RPC we don't track — pings, future methods).
func (c *Client) handleSubscribeError(id int64, errPayload json.RawMessage) {
	now := time.Now()
	type affected struct {
		channel  string
		attempts int
		failed   bool
	}
	c.mu.Lock()
	hits := make([]affected, 0)
	matched := false // any entry whatsoever under this rpcID, tombstones included
	for ch, e := range c.channels {
		if e.rpcID != id {
			continue
		}
		matched = true
		// Tombstoned channels get dropped: the server rejected the
		// subscribe AND the caller already wants this channel gone.
		// Nothing for either side to do; remove the entry quietly so
		// it doesn't sit in subUnsubAfterAck forever.
		if e.state == subUnsubAfterAck {
			delete(c.channels, ch)
			continue
		}
		if e.state != subInFlight {
			continue
		}
		e.rpcID = 0
		if e.attempts >= subRetryMax {
			e.state = subFailed
			hits = append(hits, affected{channel: ch, attempts: e.attempts, failed: true})
			continue
		}
		e.state = subPending
		e.nextTry = now.Add(subRetryDelay)
		hits = append(hits, affected{channel: ch, attempts: e.attempts, failed: false})
	}
	c.mu.Unlock()

	if len(hits) == 0 {
		if matched {
			// Matched only tombstones — caller already abandoned these
			// channels. Server's rejection of the subscribe is moot;
			// don't pollute Errs with a soft error for it.
			return
		}
		c.softErr(fmt.Errorf("server error on rpc id=%d: %s", id, errPayload))
		return
	}
	for _, h := range hits {
		if h.failed {
			c.softErr(fmt.Errorf(
				"subscribe failed for %s after %d attempts: %s",
				h.channel, subRetryMax, errPayload,
			))
		} else {
			c.softErr(fmt.Errorf(
				"subscribe rejected for %s (attempt %d/%d, retrying in %s): %s",
				h.channel, h.attempts, subRetryMax, subRetryDelay, errPayload,
			))
		}
	}
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
