package api

import (
	"fmt"
	"github.com/laevitas/cli/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreditsModeNeverAutomaticallySignsPayment(t *testing.T) {
	t.Setenv("LAEVITAS_CONFIG_DIR", t.TempDir())
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Payment-Signature") != "" || r.Header.Get("X-Payment") != "" {
			t.Error("automatic payment in credit-only mode")
		}
		w.WriteHeader(402)
		fmt.Fprint(w, `{"x402Version":2,"accepts":[]}`)
	}))
	defer server.Close()
	client := NewClient(&config.Config{BaseURL: server.URL, Auth: config.AuthTypeCredits, WalletKey: "0000000000000000000000000000000000000000000000000000000000000001"})
	if _, err := client.Get("/api/v1/test", nil); err == nil {
		t.Fatal("missing insufficient-credit refusal")
	}
	if calls != 1 {
		t.Fatal("automatically retried payment", calls)
	}
}
