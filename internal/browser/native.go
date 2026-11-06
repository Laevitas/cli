//go:build !js

package browser

import (
	"context"
	"fmt"
)

const Enabled = false

func Current(string) (Session, bool) { return Session{}, false }
func Load(string) []byte             { return nil }
func Save(string, []byte)            {}
func Clear()                         {}
func Login(context.Context, string) (Session, error) {
	return Session{}, fmt.Errorf("browser wallet is unavailable")
}
