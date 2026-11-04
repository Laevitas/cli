package agentclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	for _, mode := range []string{"reply", "payment", "rpc-error"} {
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
			if mode == "rpc-error" {
				if err == nil {
					t.Fatal("JSONRPC error accepted as success")
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
