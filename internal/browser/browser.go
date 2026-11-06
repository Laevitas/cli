// Package browser connects the browser build to its tab-local wallet session.
// Native binaries never read browser credentials or use the JavaScript bridge.
package browser

import "time"

type Session struct {
	Origin       string         `json:"origin"`
	Wallet       string         `json:"wallet"`
	Network      string         `json:"network"`
	SessionToken string         `json:"sessionToken"`
	CreditToken  string         `json:"creditToken"`
	ExpiresAt    time.Time      `json:"expiresAt"`
	Account      map[string]any `json:"account"`
}

func valid(s Session, origin string) bool {
	return s.Origin == origin && s.Wallet != "" && s.SessionToken != "" && time.Until(s.ExpiresAt) > 0 && time.Until(s.ExpiresAt) <= 16*time.Minute
}
