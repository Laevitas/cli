package wsclient

// State-machine tests for the subscribe/unsubscribe lifecycle.
//
// These tests exercise the c.channels map directly without a live
// gateway connection. They cover the interleavings that Codex
// review flagged: double unsubscribe while tombstoned, unsubscribe-
// resubscribe-before-ack, and tombstone delete on reconnect.
//
// They do NOT verify the unsubscribe RPC was written to the wire —
// activeConn is left nil so Unsubscribe takes its local-only path.
// That's enough to catch the state-machine bugs (which were the
// actual leak source); a future fake-conn harness could exercise
// the wire path too.

import (
	"context"
	"encoding/json"
	"testing"
)

// newTestClient builds a Client with no real connection, suitable
// for state-machine tests. cancel is wired up so tests don't leak
// a context goroutine.
func newTestClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		channels: make(map[string]*subEntry),
		events:   make(chan Event, 16),
		errs:     make(chan error, 16),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// TestUnsubscribeIdempotentOnTombstone catches the round-3 bug:
// double Unsubscribe of an in-flight subscribe used to delete the
// tombstone on the second call, leaking the server-side subscription
// when the ack eventually arrived.
func TestUnsubscribeIdempotentOnTombstone(t *testing.T) {
	c := newTestClient()
	defer c.cancel()

	// Set up: channel "foo" with subscribe RPC in flight.
	c.channels["foo"] = &subEntry{state: subInFlight, rpcID: 42}

	// First Unsubscribe — should tombstone, not delete.
	if err := c.Unsubscribe("foo"); err != nil {
		t.Fatalf("first Unsubscribe: %v", err)
	}
	entry, ok := c.channels["foo"]
	if !ok {
		t.Fatalf("first Unsubscribe deleted the entry; expected tombstone")
	}
	if entry.state != subUnsubAfterAck {
		t.Fatalf("after first Unsubscribe: state = %v, want subUnsubAfterAck", entry.state)
	}

	// Second Unsubscribe — must be a no-op, NOT delete the tombstone.
	if err := c.Unsubscribe("foo"); err != nil {
		t.Fatalf("second Unsubscribe: %v", err)
	}
	entry, ok = c.channels["foo"]
	if !ok {
		t.Fatalf("second Unsubscribe deleted the tombstone; subscribe ack would now leak server-side subscription")
	}
	if entry.state != subUnsubAfterAck {
		t.Fatalf("after second Unsubscribe: state = %v, want subUnsubAfterAck", entry.state)
	}
}

// TestSubscribeRestoresTombstone catches the round-2 bug: subscribe
// after Unsubscribe-while-in-flight used to be a no-op (entry exists)
// and the ack handler would then unsubscribe and delete despite the
// caller's renewed intent.
func TestSubscribeRestoresTombstone(t *testing.T) {
	c := newTestClient()
	defer c.cancel()

	c.channels["foo"] = &subEntry{state: subInFlight, rpcID: 42}

	// Unsubscribe → tombstone.
	if err := c.Unsubscribe("foo"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if c.channels["foo"].state != subUnsubAfterAck {
		t.Fatalf("expected subUnsubAfterAck after Unsubscribe")
	}

	// Subscribe again — caller changed their mind. Must restore to
	// subInFlight, preserving the original rpcID.
	if err := c.Subscribe("foo"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	entry := c.channels["foo"]
	if entry.state != subInFlight {
		t.Fatalf("after re-Subscribe: state = %v, want subInFlight", entry.state)
	}
	if entry.rpcID != 42 {
		t.Fatalf("after re-Subscribe: rpcID = %d, want 42 (preserved)", entry.rpcID)
	}

	// Now simulate the ack arriving for that original RPC. It should
	// land in subAcked because Subscribe restored the entry.
	result := json.RawMessage(`{"channels":["foo"],"subscriptionIds":["sid-1"]}`)
	c.handleSubscribeAck(42, result)
	entry = c.channels["foo"]
	if entry == nil {
		t.Fatalf("entry was deleted after ack; the renewed intent was lost")
	}
	if entry.state != subAcked {
		t.Fatalf("after ack: state = %v, want subAcked", entry.state)
	}
	if entry.subscriptionID != "sid-1" {
		t.Fatalf("after ack: subscriptionID = %q, want sid-1", entry.subscriptionID)
	}
}

// TestResetSubsForReconnectDeletesTombstones catches the round-2 bug:
// reconnect used to flip every entry to subPending, including
// tombstones — resurrecting a channel the caller explicitly removed.
func TestResetSubsForReconnectDeletesTombstones(t *testing.T) {
	c := newTestClient()
	defer c.cancel()

	c.channels["live"] = &subEntry{state: subAcked, subscriptionID: "sid-live"}
	c.channels["tombstoned"] = &subEntry{state: subUnsubAfterAck, rpcID: 7}

	c.resetSubsForReconnect()

	// Live channel should be reset to subPending; subscriptionID cleared.
	live, ok := c.channels["live"]
	if !ok {
		t.Fatalf("live channel disappeared on reset")
	}
	if live.state != subPending {
		t.Fatalf("live channel state = %v, want subPending", live.state)
	}
	if live.subscriptionID != "" {
		t.Fatalf("live channel subscriptionID = %q, want empty (would address wrong sub on new gateway)", live.subscriptionID)
	}

	// Tombstoned channel should be GONE, not resurrected as subPending.
	if _, ok := c.channels["tombstoned"]; ok {
		t.Fatalf("tombstone survived reset; would re-subscribe a channel the caller explicitly removed")
	}
}

// TestUnsubscribePendingChannel verifies that Unsubscribe of a
// channel that's never been sent to the server (still subPending)
// just drops the local entry without leaking anything.
func TestUnsubscribePendingChannel(t *testing.T) {
	c := newTestClient()
	defer c.cancel()

	c.channels["foo"] = &subEntry{state: subPending}
	if err := c.Unsubscribe("foo"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, ok := c.channels["foo"]; ok {
		t.Fatalf("subPending channel should be deleted on Unsubscribe")
	}
}

// TestUnsubscribeUnknownChannel verifies idempotency for a never-
// subscribed channel — common case during dashboard rotation where
// the new desired set drops a channel that was already filtered.
func TestUnsubscribeUnknownChannel(t *testing.T) {
	c := newTestClient()
	defer c.cancel()
	if err := c.Unsubscribe("nope"); err != nil {
		t.Fatalf("Unsubscribe of unknown channel returned error: %v", err)
	}
}

// TestHandleSubscribeAckTombstoneDropsEntry verifies that an ack
// arriving for a tombstoned entry deletes the entry from c.channels.
// (The companion behaviour — firing the unsubscribe RPC — is exercised
// indirectly by the local-state cleanup; verifying the wire write
// would need a fake conn.)
func TestHandleSubscribeAckTombstoneDropsEntry(t *testing.T) {
	c := newTestClient()
	defer c.cancel()

	c.channels["foo"] = &subEntry{state: subUnsubAfterAck, rpcID: 99}

	result := json.RawMessage(`{"channels":["foo"],"subscriptionIds":["sid-99"]}`)
	c.handleSubscribeAck(99, result)

	if _, ok := c.channels["foo"]; ok {
		t.Fatalf("tombstoned entry should be deleted after ack")
	}
}
