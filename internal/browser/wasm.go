//go:build js && wasm

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
)

const Enabled = true

func bridge() js.Value { return js.Global().Get("laevitasBrowser") }
func Current(origin string) (Session, bool) {
	var s Session
	if json.Unmarshal([]byte(bridge().Get("session").String()), &s) != nil {
		return s, false
	}
	return s, valid(s, origin)
}
func Load(key string) []byte       { return []byte(bridge().Call("load", key).String()) }
func Save(key string, data []byte) { bridge().Call("save", key, string(data)) }
func Clear()                       { bridge().Call("clear") }
func Login(ctx context.Context, origin string) (Session, error) {
	type result struct {
		raw string
		err error
	}
	ch := make(chan result, 1)
	var ok, bad js.Func
	release := func() { ok.Release(); bad.Release() }
	ok = js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer release()
		ch <- result{raw: args[0].String()}
		return nil
	})
	bad = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer release()
		ch <- result{err: fmt.Errorf("wallet sign-in was cancelled or unavailable")}
		return nil
	})
	bridge().Call("login", origin).Call("then", ok).Call("catch", bad)
	select {
	case r := <-ch:
		var s Session
		if r.err != nil {
			return s, r.err
		}
		if json.Unmarshal([]byte(r.raw), &s) != nil || !valid(s, origin) {
			return s, fmt.Errorf("invalid browser wallet session")
		}
		return s, nil
	case <-ctx.Done():
		return Session{}, ctx.Err()
	}
}
