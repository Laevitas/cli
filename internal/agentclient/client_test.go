package agentclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/laevitas/cli/internal/config"
)

const testPrivateKey = "0000000000000000000000000000000000000000000000000000000000000001"

func testClient(t *testing.T, origin string) *Client {
	t.Helper()
	t.Setenv("LAEVITAS_CONFIG_DIR", t.TempDir())
	c, e := New(&config.Config{BaseURL: origin, WalletKey: testPrivateKey, Auth: config.AuthTypeCredits})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestSignInValidatesAudienceAndKeepsSecretsPrivate(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			key, _ := crypto.HexToECDSA(testPrivateKey)
			wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
			verified := 0
			var origin, signInMessage string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == prefix+"auth/challenge" {
					now := time.Now().UTC()
					issued := now.Format(time.RFC3339Nano)
					expires := now.Add(5 * time.Minute).Format(time.RFC3339Nano)
					nonce := strings.Repeat("a", 32)
					u, _ := url.Parse(origin)
					signInMessage = fmt.Sprintf("%s wants you to sign in with your Ethereum account:\n%s\n\nSign in to Laevitas Agents. This does not authorize a payment or access to a dashboard account.\n\nURI: %s/api/v1/agents\nVersion: 1\nChain ID: 8453\nNonce: %s\nIssued At: %s\nExpiration Time: %s", u.Host, wallet, origin, nonce, issued, expires)
					audience := origin
					if bad {
						audience = "https://other.example"
					}
					json.NewEncoder(w).Encode(map[string]any{"id": newUUID(), "token": strings.Repeat("b", 64), "audience": audience, "kind": "base", "address": wallet, "network": "eip155:8453", "message": signInMessage, "nonce": nonce, "issuedAt": issued, "expiresAt": expires})
					return
				}
				if r.URL.Path == prefix+"auth/verify" {
					verified++
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					sig, _ := hex.DecodeString(strings.TrimPrefix(body["signature"], "0x"))
					if len(sig) != 65 {
						t.Error("invalid signature")
						w.WriteHeader(400)
						return
					}
					sig[64] -= 27
					pub, err := crypto.SigToPub(accounts.TextHash([]byte(signInMessage)), sig)
					if err != nil || crypto.PubkeyToAddress(*pub).Hex() != wallet {
						t.Error("wrong signer")
					}
					json.NewEncoder(w).Encode(map[string]any{"sessionToken": "lva_session_" + strings.Repeat("s", 43), "creditToken": "private-credit-token", "expiresAt": time.Now().Add(15 * time.Minute).Format(time.RFC3339Nano), "account": map[string]any{"wallet": wallet}})
					return
				}
				t.Errorf("unexpected endpoint %s", r.URL.Path)
				w.WriteHeader(404)
			}))
			defer server.Close()
			origin = server.URL
			c := testClient(t, origin)
			result, err := c.Login(context.Background())
			if bad {
				if err == nil || verified != 0 {
					t.Fatal("signed a challenge for a different audience")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "private-credit-token") || strings.Contains(string(raw), "lva_session_") {
				t.Fatal("secret printed")
			}
			info, err := os.Stat(c.file)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("state must be private")
			}
			if config.LoadCreditTokenFor(origin, wallet) != "private-credit-token" {
				t.Fatal("missing scoped token")
			}
		})
	}
}
func TestPayChecksCapBeforeNetworkAndPollsUncertainWithoutSigning(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Error("uncertain payment was resubmitted")
		}
		fmt.Fprint(w, `{"state":"uncertain"}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL)
	id := newUUID()
	c.state.Checkouts[id] = checkout{Token: strings.Repeat("a", 64), AmountCents: 10}
	if _, err := c.Pay(context.Background(), id, "0.09"); err == nil || requests != 0 {
		t.Fatal("cap check performed after request")
	}
	result, err := c.Pay(context.Background(), id, "0.10")
	if err != nil || result["state"] != "uncertain" || requests != 1 {
		t.Fatal("must return existing uncertain status", result, err, requests)
	}
}
func TestPaymentChallengeRejectsWrongResourceAmountAssetAndNetwork(t *testing.T) {
	q := checkout{PaymentURL: "https://example.com/api/v1/x402/agents/checkouts/id/pay", AmountCents: 10}
	valid := map[string]any{"scheme": "exact", "network": "eip155:8453", "asset": "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", "amount": "100000", "payTo": "0x0000000000000000000000000000000000000002"}
	for _, field := range []string{"valid", "url", "amount", "asset", "network"} {
		t.Run(field, func(t *testing.T) {
			offer := map[string]any{}
			for k, v := range valid {
				offer[k] = v
			}
			resource := q.PaymentURL
			if field == "url" {
				resource = "https://attacker.example/pay"
			} else if field != "valid" {
				offer[field] = "wrong"
			}
			body, _ := json.Marshal(map[string]any{"x402Version": 2, "resource": map[string]string{"url": resource}, "accepts": []any{offer}})
			_, err := checkedPayment(body, "", q, "eip155:8453", big.NewInt(100000))
			if (err == nil) != (field == "valid") {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
func TestRefusesRedirectsAndMCPDoesNotBuyCredits(t *testing.T) {
	received := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received++ }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/mcp" {
			if r.Header.Get("Payment-Signature") != "" {
				t.Error("signed payment from MCP")
			}
			w.WriteHeader(402)
			fmt.Fprint(w, `{"x402Version":2}`)
			return
		}
		http.Redirect(w, r, target.URL, 302)
	}))
	defer server.Close()
	c := testClient(t, server.URL)
	if _, err := c.Catalog(context.Background()); err == nil || received != 0 {
		t.Fatal("followed redirect")
	}
	c.state.Session = "local-session"
	c.state.ExpiresAt = time.Now().Add(time.Minute)
	var out bytes.Buffer
	if err := c.MCP(context.Background(), strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"tools/call\"}\n"), &out); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if json.Unmarshal(out.Bytes(), &reply) != nil || reply["id"] != float64(7) || reply["error"] == nil {
		t.Fatalf("expected structured payment refusal: %s", out.String())
	}
}

func TestClaimRequiresWalletSessionAndNeverAcceptsAnAddressOrPayment(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != prefix+"credits/claim" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer verified-wallet-session" || r.Header.Get("Payment-Signature") != "" {
			t.Error("claim must use the verified wallet session only")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 0 {
			t.Error("claim must not send a wallet or amount")
		}
		fmt.Fprint(w, `{"claimed":true,"alreadyClaimed":false,"creditsGranted":1000,"creditsRemaining":1000}`)
	}))
	defer server.Close()
	c := testClient(t, server.URL)
	c.state.Session = "verified-wallet-session"
	c.state.ExpiresAt = time.Now().Add(time.Minute)
	if result, err := c.Claim(context.Background()); err != nil || result["creditsGranted"] != float64(1000) {
		t.Fatalf("claim: %v %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("unexpected requests: %d", calls)
	}
	c.cfg.WalletKey = ""
	c.wallet = ""
	c.state.Session = ""
	if _, err := c.Claim(context.Background()); err == nil || calls != 1 {
		t.Fatal("unverified client attempted a claim")
	}
}
