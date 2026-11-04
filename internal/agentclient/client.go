// Package agentclient connects wallet identities to the agent catalog and checkout.
// It never signs payments except through Pay with an explicit spending limit.
package agentclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/x402"
)

const prefix = "/api/v1/agents/"

var uuidPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[1-8][a-fA-F0-9]{3}-[89abAB][a-fA-F0-9]{3}-[a-fA-F0-9]{12}$`)
var productPattern = regexp.MustCompile(`^(agent-enterprise|credits-[a-z0-9-]{1,30})$`)

type checkout struct {
	Token       string `json:"token"`
	AmountCents int64  `json:"amount_cents"`
	PaymentURL  string `json:"payment_url"`
	StatusURL   string `json:"status_url"`
}
type state struct {
	Origin    string              `json:"origin"`
	Wallet    string              `json:"wallet"`
	Network   string              `json:"network"`
	Session   string              `json:"session,omitempty"`
	ExpiresAt time.Time           `json:"expires_at"`
	Checkouts map[string]checkout `json:"checkouts"`
}
type Client struct {
	cfg    *config.Config
	origin string
	wallet string
	file   string
	state  state
	http   *http.Client
}

func New(cfg *config.Config) (*Client, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return nil, fmt.Errorf("agent access requires an HTTPS API origin")
	}
	c := &Client{cfg: cfg, origin: strings.ToLower(u.Scheme + "://" + u.Host), http: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	if cfg.WalletKey != "" {
		key, err := crypto.HexToECDSA(strings.TrimPrefix(cfg.WalletKey, "0x"))
		if err != nil {
			return nil, fmt.Errorf("invalid local wallet key")
		}
		c.wallet = crypto.PubkeyToAddress(key.PublicKey).Hex()
	}
	path, err := config.Path()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(c.origin + "\n" + strings.ToLower(c.wallet)))
	c.file = filepath.Join(filepath.Dir(path), "agent-"+hex.EncodeToString(sum[:])+".json")
	c.state = state{Origin: c.origin, Wallet: c.wallet, Checkouts: map[string]checkout{}}
	data, err := os.ReadFile(c.file)
	if err == nil {
		if json.Unmarshal(data, &c.state) != nil {
			return nil, fmt.Errorf("agent credential file is invalid")
		}
		if c.state.Origin != c.origin || !strings.EqualFold(c.state.Wallet, c.wallet) {
			return nil, fmt.Errorf("agent credential origin or wallet mismatch")
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read agent credential file")
	}
	if c.state.Checkouts == nil {
		c.state.Checkouts = map[string]checkout{}
	}
	return c, nil
}
func (c *Client) save() error {
	data, err := json.Marshal(c.state)
	if err != nil {
		return err
	}
	return config.WritePrivateFile(c.file, data)
}
func (c *Client) request(ctx context.Context, method, path string, body any, headers map[string]string) ([]byte, *http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("agent request interrupted; check payment status before paying again")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return nil, res, fmt.Errorf("agent response is incomplete or too large")
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return nil, res, fmt.Errorf("agent redirects are refused")
	}
	return data, res, nil
}
func decode(data []byte, res *http.Response) (map[string]any, error) {
	var result map[string]any
	if json.Unmarshal(data, &result) != nil {
		return nil, fmt.Errorf("invalid agent response (HTTP %d)", res.StatusCode)
	}
	if res.StatusCode >= 400 {
		message, _ := result["message"].(string)
		if message == "" {
			message = "agent request failed"
		}
		if len(message) > 400 {
			message = message[:400]
		}
		return nil, fmt.Errorf("%s (HTTP %d)", message, res.StatusCode)
	}
	return result, nil
}
func (c *Client) call(ctx context.Context, method, path string, body any, authenticated bool) (map[string]any, error) {
	headers := map[string]string{}
	if authenticated {
		if err := c.ensureSession(ctx); err != nil {
			return nil, err
		}
		headers["Authorization"] = "Bearer " + c.state.Session
	}
	data, res, err := c.request(ctx, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	return decode(data, res)
}
func (c *Client) ensureSession(ctx context.Context) error {
	if c.state.Session != "" && time.Until(c.state.ExpiresAt) > 30*time.Second {
		return nil
	}
	_, err := c.Login(ctx)
	return err
}
func (c *Client) Login(ctx context.Context) (map[string]any, error) {
	if c.wallet == "" {
		return nil, fmt.Errorf("configure a wallet with laevitas wallet init or LAEVITAS_WALLET_KEY")
	}
	challenge, err := c.call(ctx, "POST", prefix+"auth/challenge", map[string]string{"kind": "base", "address": c.wallet}, false)
	if err != nil {
		return nil, err
	}
	field := func(name string) string { v, _ := challenge[name].(string); return v }
	issued, ie := time.Parse(time.RFC3339Nano, field("issuedAt"))
	expires, ee := time.Parse(time.RFC3339Nano, field("expiresAt"))
	nonce := field("nonce")
	network := field("network")
	message := field("message")
	origin, _ := url.Parse(c.origin)
	expected := fmt.Sprintf("%s wants you to sign in with your Ethereum account:\n%s\n\nSign in to Laevitas Agents. This does not authorize a payment or access to a dashboard account.\n\nURI: %s/api/v1/agents\nVersion: 1\nChain ID: %s\nNonce: %s\nIssued At: %s\nExpiration Time: %s", origin.Host, field("address"), c.origin, strings.TrimPrefix(network, "eip155:"), nonce, field("issuedAt"), field("expiresAt"))
	if field("audience") != c.origin || field("kind") != "base" || !strings.EqualFold(field("address"), c.wallet) ||
		(network != "eip155:8453" && network != "eip155:84532") || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(nonce) ||
		!uuidPattern.MatchString(field("id")) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(field("token")) ||
		ie != nil || ee != nil || time.Since(issued) > time.Minute || time.Until(issued) > time.Minute || time.Until(expires) <= 0 || !expires.After(issued) || expires.Sub(issued) > 5*time.Minute || message != expected {
		return nil, fmt.Errorf("wallet sign-in challenge did not match the API origin, wallet, network or expiry")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(c.cfg.WalletKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid wallet key")
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(message)), key)
	if err != nil {
		return nil, err
	}
	signature[64] += 27
	result, err := c.call(ctx, "POST", prefix+"auth/verify", map[string]string{"challengeId": field("id"), "challengeToken": field("token"), "signature": "0x" + hex.EncodeToString(signature)}, false)
	if err != nil {
		return nil, err
	}
	session, _ := result["sessionToken"].(string)
	expiry, _ := result["expiresAt"].(string)
	expires, err = time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !regexp.MustCompile(`^lva_session_[A-Za-z0-9_-]{43}$`).MatchString(session) || time.Until(expires) <= 0 || time.Until(expires) > 16*time.Minute {
		return nil, fmt.Errorf("invalid agent session")
	}
	c.state.Session = session
	c.state.ExpiresAt = expires
	c.state.Network = network
	if err = c.save(); err != nil {
		return nil, err
	}
	if token, ok := result["creditToken"].(string); ok && token != "" {
		if err = config.SaveCreditTokenFor(c.origin, c.wallet, token); err != nil {
			return nil, err
		}
	}
	return map[string]any{"account": result["account"], "expiresAt": expiry, "authenticated": true}, nil
}
func (c *Client) Catalog(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "GET", prefix+"catalog", nil, false)
}
func (c *Client) Sample(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "GET", prefix+"sandbox/market-snapshot", nil, false)
}
func (c *Client) Account(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "GET", prefix+"me", nil, true)
}
func (c *Client) Claim(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "POST", prefix+"credits/claim", map[string]any{}, true)
}
func (c *Client) Balance(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "GET", prefix+"credits", nil, true)
}
func (c *Client) Usage(ctx context.Context, before string) (map[string]any, error) {
	path := prefix + "usage"
	if before != "" {
		path += "?before=" + url.QueryEscape(before)
	}
	return c.call(ctx, "GET", path, nil, true)
}
func (c *Client) Logout(ctx context.Context) (map[string]any, error) {
	result, err := c.call(ctx, "POST", prefix+"auth/logout", nil, true)
	if err != nil {
		return nil, err
	}
	c.state.Session = ""
	c.state.ExpiresAt = time.Time{}
	config.ClearCreditTokenFor(c.origin, c.wallet)
	return result, c.save()
}
func (c *Client) Keys(ctx context.Context) (map[string]any, error) {
	return c.call(ctx, "GET", prefix+"keys", nil, true)
}
func (c *Client) CreateKey(ctx context.Context, label string) (map[string]any, error) {
	if strings.TrimSpace(label) == "" || len(label) > 80 {
		return nil, fmt.Errorf("key label must be 1–80 characters")
	}
	return c.call(ctx, "POST", prefix+"keys", map[string]string{"label": label}, true)
}
func (c *Client) RevokeKey(ctx context.Context, id string) (map[string]any, error) {
	if !uuidPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid key ID")
	}
	return c.call(ctx, "DELETE", prefix+"keys/"+id, nil, true)
}
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func (c *Client) Quote(ctx context.Context, product, cycle, requestID string) (map[string]any, error) {
	if !productPattern.MatchString(product) {
		return nil, fmt.Errorf("invalid product ID; use agent catalog")
	}
	if requestID == "" {
		requestID = newUUID()
	}
	if !uuidPattern.MatchString(requestID) {
		return nil, fmt.Errorf("request-id must be a UUID")
	}
	input := map[string]string{"productId": product, "requestId": requestID}
	if product == "agent-enterprise" {
		input["cycle"] = cycle
	}
	result, err := c.call(ctx, "POST", prefix+"quotes", input, true)
	if err != nil {
		return nil, fmt.Errorf("%w; retry with --request-id %s", err, requestID)
	}
	id, _ := result["id"].(string)
	token, _ := result["checkoutToken"].(string)
	paymentURL, _ := result["paymentUrl"].(string)
	statusURL, _ := result["statusUrl"].(string)
	q, _ := result["quote"].(map[string]any)
	cents, _ := q["amountCents"].(float64)
	expected := c.origin + "/api/v1/x402/agents/checkouts/" + id
	if !uuidPattern.MatchString(id) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(token) || paymentURL != expected+"/pay" || statusURL != expected || cents < 1 || cents > 10000000 || cents != float64(int64(cents)) {
		return nil, fmt.Errorf("invalid checkout response")
	}
	c.state.Checkouts[id] = checkout{token, int64(cents), paymentURL, statusURL}
	if err = c.save(); err != nil {
		return nil, err
	}
	delete(result, "checkoutToken")
	result["requestId"] = requestID
	return result, nil
}
func (c *Client) Status(ctx context.Context, id string) (map[string]any, error) {
	if !uuidPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid checkout ID")
	}
	if q, ok := c.state.Checkouts[id]; ok {
		data, res, err := c.request(ctx, "GET", "/api/v1/x402/agents/checkouts/"+id, nil, map[string]string{"X-Checkout-Token": q.Token})
		if err != nil {
			return nil, err
		}
		return decode(data, res)
	}
	return c.call(ctx, "GET", prefix+"checkouts/"+id, nil, true)
}
func usdcAtomic(value string) (*big.Int, error) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]{0,7})(\.[0-9]{1,6})?$`).MatchString(value) {
		return nil, fmt.Errorf("max-usdc must be a positive decimal with at most six fractional digits")
	}
	parts := strings.Split(value, ".")
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	n, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 6-len(fraction)), 10)
	if !ok || n.Sign() <= 0 {
		return nil, fmt.Errorf("max-usdc must be greater than zero")
	}
	return n, nil
}
func (c *Client) Pay(ctx context.Context, id, maxUSDC string) (map[string]any, error) {
	cap, err := usdcAtomic(maxUSDC)
	if err != nil {
		return nil, err
	}
	q, ok := c.state.Checkouts[id]
	if !ok || !uuidPattern.MatchString(id) {
		return nil, fmt.Errorf("quote this checkout on this device first")
	}
	if c.wallet == "" {
		return nil, fmt.Errorf("wallet is not configured")
	}
	if new(big.Int).Mul(big.NewInt(q.AmountCents), big.NewInt(10000)).Cmp(cap) > 0 {
		return nil, fmt.Errorf("quoted price exceeds --max-usdc; no payment was signed")
	}
	status, err := c.Status(ctx, id)
	if err != nil {
		return nil, err
	}
	if status["state"] != "quoted" {
		return status, nil
	}
	path := "/api/v1/x402/agents/checkouts/" + id + "/pay"
	headers := map[string]string{"X-Checkout-Token": q.Token}
	data, res, err := c.request(ctx, "POST", path, nil, headers)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != 402 {
		return decode(data, res)
	}
	filtered, err := checkedPayment(data, res.Header.Get("Payment-Required"), q, c.state.Network, cap)
	if err != nil {
		return nil, err
	}
	// Pass the single validated Base offer to the official x402 signer.
	responseCopy := *res
	responseCopy.Header = res.Header.Clone()
	responseCopy.Header.Set("Payment-Required", base64.StdEncoding.EncodeToString(filtered))
	payer, err := x402.NewPaymentClient(c.cfg.WalletKey)
	if err != nil {
		return nil, fmt.Errorf("invalid wallet signer")
	}
	signed, err := payer.HandlePaymentRequired(&responseCopy, filtered)
	if err != nil {
		return nil, fmt.Errorf("could not sign the quoted payment: %w", err)
	}
	for name, value := range signed {
		headers[name] = value
	}
	data, res, err = c.request(ctx, "POST", path, nil, headers)
	if err != nil {
		return nil, fmt.Errorf("%w; run laevitas agent status %s", err, id)
	}
	return decode(data, res)
}
func checkedPayment(body []byte, header string, q checkout, network string, cap *big.Int) ([]byte, error) {
	if header != "" {
		var err error
		body, err = base64.StdEncoding.DecodeString(header)
		if err != nil {
			return nil, fmt.Errorf("invalid payment challenge")
		}
	}
	var required map[string]any
	if json.Unmarshal(body, &required) != nil || required["x402Version"] != float64(2) {
		return nil, fmt.Errorf("x402 v2 payment required")
	}
	resource, _ := required["resource"].(map[string]any)
	if resource["url"] != q.PaymentURL {
		return nil, fmt.Errorf("payment resource does not match the checkout")
	}
	assets := map[string]string{"eip155:8453": "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", "eip155:84532": "0x036cbd53842c5426634e7929541ec2318f3dcf7e"}
	expectedAsset, ok := assets[network]
	if !ok {
		return nil, fmt.Errorf("sign in again to confirm the payment network")
	}
	offers, _ := required["accepts"].([]any)
	for _, item := range offers {
		offer, ok := item.(map[string]any)
		if !ok || offer["network"] != network || offer["scheme"] != "exact" {
			continue
		}
		asset, _ := offer["asset"].(string)
		amount, _ := offer["amount"].(string)
		payTo, _ := offer["payTo"].(string)
		units, ok := new(big.Int).SetString(amount, 10)
		if !ok || units.Sign() <= 0 || units.Cmp(cap) > 0 || amount != fmt.Sprint(q.AmountCents*10000) || strings.ToLower(asset) != expectedAsset || !regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`).MatchString(payTo) {
			return nil, fmt.Errorf("payment amount, USDC asset or recipient does not match the approved quote")
		}
		required["accepts"] = []any{offer}
		return json.Marshal(required)
	}
	return nil, fmt.Errorf("no supported Base USDC offer matches this wallet")
}

// MCP proxies structured messages using credits already purchased by the owner.
// It never makes an on-chain payment and never puts wallet secrets into tool output.
func (c *Client) MCP(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var message map[string]any
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			return fmt.Errorf("invalid MCP JSON message")
		}
		headers := map[string]string{}
		if c.cfg.APIKey != "" && c.cfg.Auth != config.AuthTypeX402 {
			headers["X-API-Key"] = c.cfg.APIKey
		} else {
			if err := c.ensureSession(ctx); err != nil {
				return err
			}
			headers["X-Credit-Token"] = config.LoadCreditTokenFor(c.origin, c.wallet)
		}
		data, res, err := c.request(ctx, "POST", "/api/v1/mcp", message, headers)
		if _, hasID := message["id"]; !hasID {
			if err != nil {
				return err
			}
			continue
		}
		var reply any
		if err != nil || res.StatusCode >= 400 {
			status := 0
			if res != nil {
				status = res.StatusCode
			}
			reply = map[string]any{"jsonrpc": "2.0", "id": message["id"], "error": map[string]any{"code": -32002, "message": "Laevitas request unavailable. Check agent balance and payment status; purchase credits explicitly when needed.", "data": map[string]any{"httpStatus": status}}}
		} else if json.Unmarshal(data, &reply) != nil {
			return fmt.Errorf("MCP server did not return JSON")
		}
		if err = json.NewEncoder(out).Encode(reply); err != nil {
			return err
		}
	}
	return scanner.Err()
}
