package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum/crypto"
)

type partnerCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Interfaces  []struct {
		URL     string `json:"url"`
		Binding string `json:"protocolBinding"`
		Version string `json:"protocolVersion"`
	} `json:"supportedInterfaces"`
	Capabilities map[string]bool `json:"capabilities"`
	Input        []string        `json:"defaultInputModes"`
	Output       []string        `json:"defaultOutputModes"`
	Skills       []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	} `json:"skills"`
}
type partner struct {
	origin, endpoint, signer string
	discovery                map[string]any
	card                     partnerCard
	http                     *http.Client
}

// Verify6022 verifies detached ES256K JWS integrity. Registry ownership is a separate check.
func Verify6022(raw []byte, expectedSigner string) (string, error) {
	if len(raw) > 1024*1024 {
		return "", fmt.Errorf("6022 document is too large")
	}
	canonical, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return "", fmt.Errorf("invalid canonical 6022 JSON")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(canonical, &document) != nil {
		return "", fmt.Errorf("invalid 6022 document")
	}
	var signatures []struct {
		Protected string `json:"protected"`
		Signature string `json:"signature"`
	}
	if json.Unmarshal(document["signatures"], &signatures) != nil || len(signatures) < 1 || len(signatures) > 8 {
		return "", fmt.Errorf("signed 6022 discovery is required")
	}
	delete(document, "signatures")
	unsigned, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	canonical, err = jsoncanonicalizer.Transform(unsigned)
	if err != nil {
		return "", err
	}
	for _, signature := range signatures {
		var header struct {
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			Typ string `json:"typ"`
			JWK struct {
				Crv string `json:"crv"`
				Kty string `json:"kty"`
				X   string `json:"x"`
				Y   string `json:"y"`
			} `json:"jwk"`
		}
		encoded, err := base64.RawURLEncoding.DecodeString(signature.Protected)
		if err != nil {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&header) != nil || header.Alg != "ES256K" || header.Typ != "JOSE" || header.JWK.Crv != "secp256k1" || header.JWK.Kty != "EC" {
			continue
		}
		x, xe := base64.RawURLEncoding.DecodeString(header.JWK.X)
		y, ye := base64.RawURLEncoding.DecodeString(header.JWK.Y)
		sig, se := base64.RawURLEncoding.DecodeString(signature.Signature)
		if xe != nil || ye != nil || se != nil || len(x) != 32 || len(y) != 32 || len(sig) != 64 {
			continue
		}
		public := append(append([]byte{4}, x...), y...)
		key, err := crypto.UnmarshalPubkey(public)
		if err != nil {
			continue
		}
		signer := crypto.PubkeyToAddress(*key).Hex()
		if signer != header.Kid || (expectedSigner != "" && !strings.EqualFold(signer, expectedSigner)) {
			continue
		}
		digest := sha256.Sum256([]byte(signature.Protected + "." + base64.RawURLEncoding.EncodeToString(canonical)))
		if crypto.VerifySignature(public, digest[:], sig) {
			return signer, nil
		}
	}
	return "", fmt.Errorf("6022 signature or expected signer did not match")
}
func inspectPartner(ctx context.Context, origin, expectedSigner string) (*partner, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return nil, fmt.Errorf("partner origin must be HTTPS without a path")
	}
	p := &partner{origin: u.Scheme + "://" + u.Host, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	fetch := func(path string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", p.origin+path, nil)
		if err != nil {
			return nil, err
		}
		res, err := p.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("partner discovery unavailable")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("partner discovery returned HTTP %d", res.StatusCode)
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
		if err != nil || len(raw) > 1024*1024 {
			return nil, fmt.Errorf("partner document is incomplete or too large")
		}
		return raw, nil
	}
	raw, err := fetch("/.well-known/6022")
	if err != nil {
		return nil, err
	}
	p.signer, err = Verify6022(raw, expectedSigner)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &p.discovery) != nil || p.discovery["protocol"] != "6022" || p.discovery["protocolVersion"] != "0.1" {
		return nil, fmt.Errorf("unsupported 6022 discovery version")
	}
	raw, err = fetch("/.well-known/agent-card.json")
	if err != nil {
		return nil, err
	}
	if _, err = Verify6022(raw, p.signer); err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &p.card) != nil || p.card.Name == "" || p.card.Description == "" || p.card.Version == "" || p.card.Capabilities == nil || len(p.card.Input) == 0 || len(p.card.Output) == 0 || len(p.card.Skills) == 0 {
		return nil, fmt.Errorf("incomplete A2A agent card")
	}
	ids := map[string]bool{}
	for _, skill := range p.card.Skills {
		if skill.ID == "" || skill.Name == "" || skill.Description == "" || skill.Tags == nil || ids[skill.ID] {
			return nil, fmt.Errorf("invalid A2A skills")
		}
		ids[skill.ID] = true
	}
	for _, binding := range p.card.Interfaces {
		if binding.Binding == "JSONRPC" && binding.Version == "1.0" {
			endpoint, e := url.Parse(binding.URL)
			if e == nil && endpoint.Scheme == u.Scheme && strings.EqualFold(endpoint.Host, u.Host) && endpoint.User == nil && endpoint.RawQuery == "" && endpoint.Fragment == "" {
				p.endpoint = endpoint.String()
				break
			}
		}
	}
	if p.endpoint == "" {
		return nil, fmt.Errorf("no supported same-origin A2A 1.0 JSONRPC endpoint")
	}
	return p, nil
}
func InspectPartner(ctx context.Context, origin, signer string) (map[string]any, error) {
	return InspectPartnerWithRPC(ctx, origin, signer, "")
}
func InspectPartnerWithRPC(ctx context.Context, origin, signer, rpcURL string) (map[string]any, error) {
	p, err := inspectPartner(ctx, origin, signer)
	if err != nil {
		return nil, err
	}
	registry := verifyPartnerRegistry(ctx, p, rpcURL)
	return map[string]any{"origin": p.origin, "name": p.card.Name, "endpoint": p.endpoint, "signer": p.signer,
		"signatureVerified": true, "matchesExpectedSigner": signer != "", "registryVerified": registry.Verified, "registry": registry, "liveExchangeVerified": false,
		"ensDomain": p.discovery["ensDomain"], "paymentMethods": p.discovery["paymentMethods"], "skills": p.card.Skills,
		"next": "Registry checks read the canonical 6022 chain directly; L1 ENS/CCIP resolution and a live A2A conversation are separate checks."}, nil
}
func AskPartner(ctx context.Context, origin, signer, message, contextID string) (map[string]any, error) {
	if signer == "" {
		return nil, fmt.Errorf("supply --signer from the verified partner identity")
	}
	if strings.TrimSpace(message) == "" || len(message) > 16000 || len(contextID) > 256 {
		return nil, fmt.Errorf("message must contain 1–16000 bytes and context-id at most 256 bytes")
	}
	p, err := inspectPartner(ctx, origin, signer)
	if err != nil {
		return nil, err
	}
	id := newUUID()
	turn := map[string]any{"messageId": newUUID(), "role": "user", "parts": []any{map[string]string{"text": message}}}
	if contextID != "" {
		turn["contextId"] = contextID
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "SendMessage", "params": map[string]any{"message": turn}})
	req, err := http.NewRequestWithContext(ctx, "POST", p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("partner exchange unavailable")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, fmt.Errorf("partner reply is incomplete or too large")
	}
	if res.StatusCode == 402 {
		if header := res.Header.Get("Payment-Required"); header != "" {
			data, err = base64.StdEncoding.DecodeString(header)
			if err != nil {
				return nil, fmt.Errorf("invalid partner payment challenge")
			}
		}
		var required map[string]any
		if json.Unmarshal(data, &required) != nil || required["x402Version"] != float64(2) {
			return nil, fmt.Errorf("invalid partner x402 challenge")
		}
		return map[string]any{"delivered": false, "state": "wallet_authorization_required", "paymentRequired": required,
			"message": "Partner authorization is separate from Laevitas credits. Even a zero-price rule requires signing. Use the partner's x402 client after approving its terms."}, nil
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("partner turn failed (HTTP %d)", res.StatusCode)
	}
	var reply struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Error   json.RawMessage `json:"error"`
		Result  struct {
			Message struct {
				MessageID string `json:"messageId"`
				Role      string `json:"role"`
				ContextID string `json:"contextId"`
				Parts     []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.JSONRPC != "2.0" || reply.ID != id || len(reply.Error) > 0 || reply.Result.Message.Role != "agent" || reply.Result.Message.MessageID == "" {
		return nil, fmt.Errorf("partner returned an invalid A2A response")
	}
	text := ""
	for _, part := range reply.Result.Message.Parts {
		text += part.Text
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("partner returned no content")
	}
	return map[string]any{"delivered": true, "text": text, "messageId": reply.Result.Message.MessageID, "contextId": reply.Result.Message.ContextID}, nil
}
