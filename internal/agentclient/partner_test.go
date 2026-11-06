package agentclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// Sorted ASCII keys below are an independently specified JCS test payload.
func signedDocument(unsigned string, wrongHash bool) []byte {
	key, _ := crypto.HexToECDSA(testPrivateKey)
	pub := crypto.FromECDSAPub(&key.PublicKey)
	b64 := base64.RawURLEncoding.EncodeToString
	header := fmt.Sprintf(`{"alg":"ES256K","jwk":{"crv":"secp256k1","kty":"EC","x":"%s","y":"%s"},"kid":"%s","typ":"JOSE"}`, b64(pub[1:33]), b64(pub[33:]), crypto.PubkeyToAddress(key.PublicKey).Hex())
	protected := b64([]byte(header))
	input := []byte(protected + "." + b64([]byte(unsigned)))
	hash := sha256.Sum256(input)
	digest := hash[:]
	if wrongHash {
		digest = crypto.Keccak256(input)
	}
	sig, _ := crypto.Sign(digest, key)
	return []byte(strings.TrimSuffix(unsigned, "}") + `,"signatures":[{"protected":"` + protected + `","signature":"` + b64(sig[:64]) + `"}]}`)
}
func Test6022DetachedJWSUsesSHA256AndRejectsTampering(t *testing.T) {
	raw := signedDocument(`{"protocol":"6022","protocolVersion":"0.1"}`, false)
	signer, err := Verify6022(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Verify6022(raw, signer); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(strings.Replace(string(raw), `"0.1"`, `"0.2"`, 1)), signedDocument(`{"protocol":"6022","protocolVersion":"0.1"}`, true), []byte(`{"protocol":"6022"}`)} {
		if _, err = Verify6022(bad, signer); err == nil {
			t.Fatal("accepted tampered/unsigned/wrong-hash document")
		}
	}
	if _, err = Verify6022(raw, "0x0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("accepted wrong identity")
	}
}
func TestPartnerUsesAdvertisedInterfaceAndNeverPays(t *testing.T) {
	for _, mode := range []string{"reply", "payment", "rpc-error", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var origin string
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Payment-Signature") != "" || r.Header.Get("X-Credit-Token") != "" {
					t.Error("Laevitas credentials/payment leaked to partner")
				}
				switch r.URL.Path {
				case "/.well-known/6022":
					w.Write(signedDocument(`{"protocol":"6022","protocolVersion":"0.1"}`, false))
				case "/.well-known/agent-card.json":
					w.Write(signedDocument(fmt.Sprintf(`{"capabilities":{},"defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],"description":"Test agent","name":"Example","skills":[{"description":"Echo text","id":"echo","name":"Echo","tags":[]}],"supportedInterfaces":[{"protocolBinding":"JSONRPC","protocolVersion":"1.0","url":"%s/api/custom"}],"version":"1"}`, origin), false))
				case "/api/custom":
					calls++
					if mode == "redirect" {
						w.Header().Set("Location", origin+"/not-advertised")
						w.WriteHeader(http.StatusTemporaryRedirect)
						return
					}
					var request map[string]any
					json.NewDecoder(r.Body).Decode(&request)
					if request["method"] != "SendMessage" {
						t.Error("wrong A2A method")
					}
					if mode == "payment" {
						w.WriteHeader(402)
						fmt.Fprint(w, `{"x402Version":2,"accepts":[]}`)
						return
					}
					if mode == "rpc-error" {
						json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "error": map[string]any{"code": -1}})
						return
					}
					params := request["params"].(map[string]any)
					msg := params["message"].(map[string]any)
					if msg["role"] != "user" || msg["contextId"] != "test-context" || msg["messageId"] == "" {
						t.Error("invalid A2A message")
					}
					json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{"message": map[string]any{"messageId": "reply-1", "role": "agent", "contextId": "test-context", "parts": []any{map[string]string{"text": "Echo: nonce-739"}}}}})
				default:
					t.Errorf("unadvertised endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			origin = server.URL
			inspected, err := InspectPartner(context.Background(), origin, "")
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 || inspected["registryVerified"] != false || inspected["liveExchangeVerified"] != false {
				t.Fatal("inspection overclaimed verification")
			}
			result, err := AskPartner(context.Background(), origin, inspected["signer"].(string), "nonce-739", "test-context")
			if mode == "rpc-error" || mode == "redirect" {
				if err == nil {
					t.Fatal("JSONRPC error or redirect accepted as success")
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatal(result, err, calls)
			}
			if (result["delivered"] == true) != (mode == "reply") {
				t.Fatal("incorrect delivery status", result)
			}
		})
	}
}

type partnerTransportFunc func(*http.Request) (*http.Response, error)

func (f partnerTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPartnerDiscoveryAliasRequiresPinnedSignerAndExactDestination(t *testing.T) {
	discovery := signedDocument(`{"protocol":"6022","protocolVersion":"0.1"}`, false)
	signer, err := Verify6022(discovery, "")
	if err != nil {
		t.Fatal(err)
	}
	card := func(endpoint string) []byte {
		return signedDocument(fmt.Sprintf(`{"capabilities":{},"defaultInputModes":["text/plain"],"defaultOutputModes":["text/plain"],"description":"Test agent","name":"Welcome","skills":[{"description":"Echo text","id":"echo","name":"Echo","tags":[]}],"supportedInterfaces":[{"protocolBinding":"JSONRPC","protocolVersion":"1.0","url":"%s"}],"version":"1"}`, endpoint), false)
	}
	cases := []struct {
		name, origin, pin, location, endpoint, wantError string
		status, wantCalls                                int
		secondHop, cardAtAlias, tamperedCard             bool
	}{
		{name: "verified alias", wantCalls: 4},
		{name: "direct canonical origin", origin: laevitasWelcomeOrigin, wantCalls: 2},
		{name: "missing signer", pin: "none", wantError: "requires its exact HTTPS destination and --signer", wantCalls: 1},
		{name: "wrong signer", pin: "0x0000000000000000000000000000000000000000", wantError: "expected signer did not match", wantCalls: 2},
		{name: "untrusted alias", origin: "https://other.example", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "wrong status", status: http.StatusFound, wantError: "HTTP 302", wantCalls: 1},
		{name: "different host", location: "https://other.example/.well-known/6022", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "HTTP downgrade", location: strings.Replace(laevitasWelcomeOrigin, "https:", "http:", 1) + "/.well-known/6022", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "different path", location: laevitasWelcomeOrigin + "/api/a2a", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "query parameters", location: laevitasWelcomeOrigin + "/.well-known/6022?next=other", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "fragment", location: laevitasWelcomeOrigin + "/.well-known/6022#other", wantError: "untrusted discovery redirect", wantCalls: 1},
		{name: "second hop", secondHop: true, wantError: "HTTP 307", wantCalls: 2},
		{name: "mixed origins", cardAtAlias: true, wantError: "resolve to different origins", wantCalls: 3},
		{name: "tampered card", tamperedCard: true, wantError: "expected signer did not match", wantCalls: 4},
		{name: "cross-origin interface", endpoint: "https://other.example/api/a2a", wantError: "no supported same-origin", wantCalls: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origin, pin, endpoint := tc.origin, tc.pin, tc.endpoint
			if origin == "" {
				origin = laevitasDiscoveryOrigin
			}
			if pin == "" {
				pin = signer
			} else if pin == "none" {
				pin = ""
			}
			if endpoint == "" {
				endpoint = laevitasWelcomeOrigin + "/api/a2a"
			}
			calls := 0
			transport := partnerTransportFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "GET" || len(req.Header) != 0 || req.Body != nil {
					t.Fatalf("discovery carried a body, credentials or unexpected method: %s", req.Method)
				}
				res := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: req}
				raw := discovery
				isCard := req.URL.Path == "/.well-known/agent-card.json"
				if isCard {
					raw = card(endpoint)
					if tc.tamperedCard {
						raw = []byte(strings.Replace(string(raw), `"name":"Welcome"`, `"name":"Impostor"`, 1))
					}
				} else if req.URL.Path != "/.well-known/6022" {
					t.Fatalf("unexpected path: %s", req.URL.Path)
				}
				if req.URL.Scheme+"://"+req.URL.Host == laevitasWelcomeOrigin {
					if tc.secondHop {
						res.StatusCode = http.StatusTemporaryRedirect
						res.Header.Set("Location", "https://other.example"+req.URL.Path)
					}
				} else if req.URL.Scheme+"://"+req.URL.Host == origin {
					if !(isCard && tc.cardAtAlias) {
						res.StatusCode = http.StatusTemporaryRedirect
						if tc.status != 0 {
							res.StatusCode = tc.status
						}
						location := laevitasWelcomeOrigin + req.URL.Path
						if tc.location != "" {
							location = tc.location
						}
						res.Header.Set("Location", location)
						res.Header.Set("Set-Cookie", "session=never-forward; Secure")
					}
				} else {
					t.Fatalf("contacted an untrusted destination: %s", req.URL)
				}
				res.Body = io.NopCloser(strings.NewReader(string(raw)))
				return res, nil
			})
			p, err := inspectPartnerWithTransport(context.Background(), origin, pin, transport)
			if calls != tc.wantCalls {
				t.Fatalf("made %d requests, want %d", calls, tc.wantCalls)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error containing %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.discoveryOrigin != origin || p.origin != laevitasWelcomeOrigin || p.endpoint != endpoint || p.signer != signer {
				t.Fatal("discovery alias changed the signed canonical identity")
			}
		})
	}
}
