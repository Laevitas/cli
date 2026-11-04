package agentclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Official deployments.json, 6022-labs/agentic-skills@886a9be174f31f1fff50f61fa3494e9edc6598ef.
// Never discover contract/RPC addresses from an untrusted agent card.
var registryDeployments = map[string]struct{ registry, manager, rpc string }{
	"137":   {"0x23dbc335f966d9869940FB55f61E919118AAD236", "0xb0dc8a83c700A9BBcc53cA1a2C6993a63129d2F6", "https://polygon.drpc.org"},
	"80002": {"0x37c5879529E5b0241663ed40feeebb2326BBEE0d", "0x8095A0D4bE42A61db9e609aA693f516044Bda990", "https://polygon-amoy-public.nodies.app"},
}

const registryABI = `[
 {"type":"function","name":"provisioned","inputs":[{"type":"bytes32"}],"outputs":[{"type":"bool"}]},
 {"type":"function","name":"addr","inputs":[{"type":"bytes32"}],"outputs":[{"type":"address"}]},
 {"type":"function","name":"text","inputs":[{"type":"bytes32"},{"type":"string"}],"outputs":[{"type":"string"}]},
 {"type":"function","name":"agentRef","inputs":[{"type":"bytes32"}],"outputs":[{"type":"address"},{"type":"uint256"}]},
 {"type":"function","name":"manager","inputs":[],"outputs":[{"type":"address"}]},
 {"type":"function","name":"isKnownCollection","inputs":[{"type":"address"}],"outputs":[{"type":"bool"}]},
 {"type":"function","name":"collectionIdByName","inputs":[{"type":"string"}],"outputs":[{"type":"uint256"}]},
 {"type":"function","name":"collections","inputs":[{"type":"uint256"}],"outputs":[{"type":"address"}]},
 {"type":"function","name":"nameOf","inputs":[{"type":"uint256"}],"outputs":[{"type":"string"}]},
 {"type":"function","name":"ownerOf","inputs":[{"type":"uint256"}],"outputs":[{"type":"address"}]},
 {"type":"function","name":"addressToTokenId","inputs":[{"type":"string"},{"type":"string"}],"outputs":[{"type":"uint256"}]}
]`

type registryResult struct {
	Verified              bool   `json:"verified"`
	State                 string `json:"state"`
	Reason                string `json:"reason,omitempty"`
	Method                string `json:"method"`
	ChainID               string `json:"chainId,omitempty"`
	Registry              string `json:"registry,omitempty"`
	Block                 string `json:"block,omitempty"`
	Collection            string `json:"collection,omitempty"`
	TokenID               string `json:"tokenId,omitempty"`
	Owner                 string `json:"owner,omitempty"`
	ENSResolutionVerified bool   `json:"ensResolutionVerified"`
}

func nameHash(name string) [32]byte {
	var node [32]byte
	labels := strings.Split(name, ".")
	for i := len(labels) - 1; i >= 0; i-- {
		copy(node[:], crypto.Keccak256(node[:], crypto.Keccak256([]byte(labels[i]))))
	}
	return node
}

func verifyPartnerRegistry(ctx context.Context, p *partner, rpcURL string) registryResult {
	result := registryResult{State: "not_checked", Method: "6022-registry-rpc"}
	name, ok := p.discovery["ensDomain"].(string)
	if !ok || name == "" {
		result.Reason = "No ENS name is advertised."
		return result
	}
	pattern := regexp.MustCompile(`^([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.(137|80002)\.6022\.eth$`)
	labels := pattern.FindStringSubmatch(name)
	if labels == nil {
		result.Reason = "Unsupported 6022 ENS name or chain."
		return result
	}
	deployment := registryDeployments[labels[3]]
	result.ChainID, result.Registry = labels[3], deployment.registry
	if rpcURL == "" {
		rpcURL = deployment.rpc
	}
	u, err := url.Parse(rpcURL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		result.State, result.Reason = "unavailable", "Registry RPC must use HTTPS."
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	id := 0
	rpc := func(method string, params []any) (string, error) {
		id++
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
		if err != nil || len(raw) > 256*1024 || res.StatusCode != 200 {
			return "", fmt.Errorf("RPC unavailable")
		}
		var reply struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  string          `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &reply) != nil || reply.JSONRPC != "2.0" || reply.ID != id || (len(reply.Error) > 0 && string(reply.Error) != "null") || !strings.HasPrefix(reply.Result, "0x") {
			return "", fmt.Errorf("invalid RPC response")
		}
		return reply.Result, nil
	}
	unavailable := func() registryResult {
		result.State, result.Reason = "unavailable", "Registry evidence could not be read; identity is not registry-verified."
		return result
	}
	mismatch := func(reason string) registryResult { result.State, result.Reason = "mismatch", reason; return result }
	chain, err := rpc("eth_chainId", []any{})
	if err != nil {
		return unavailable()
	}
	number, valid := new(big.Int).SetString(strings.TrimPrefix(chain, "0x"), 16)
	if !valid || number.String() != labels[3] {
		return mismatch("RPC returned the wrong chain.")
	}
	block, err := rpc("eth_blockNumber", []any{})
	if err != nil {
		return unavailable()
	}
	if _, valid := new(big.Int).SetString(strings.TrimPrefix(block, "0x"), 16); !valid {
		return unavailable()
	}
	result.Block = block
	parsed, _ := abi.JSON(strings.NewReader(registryABI))
	call := func(address, method string, args ...any) ([]any, error) {
		input, err := parsed.Pack(method, args...)
		if err != nil {
			return nil, err
		}
		output, err := rpc("eth_call", []any{map[string]string{"to": address, "data": "0x" + hex.EncodeToString(input)}, block})
		if err != nil {
			return nil, err
		}
		data, err := hex.DecodeString(strings.TrimPrefix(output, "0x"))
		if err != nil {
			return nil, err
		}
		return parsed.Unpack(method, data)
	}
	node := nameHash(name)
	manager, err := call(deployment.registry, "manager")
	if err != nil {
		return unavailable()
	}
	if manager[0].(common.Address) != common.HexToAddress(deployment.manager) {
		return mismatch("Registry manager does not match the official deployment.")
	}
	provisioned, err := call(deployment.registry, "provisioned", node)
	if err != nil {
		return unavailable()
	}
	if !provisioned[0].(bool) {
		return mismatch("ENS name is not provisioned in the 6022 registry.")
	}
	signer, err := call(deployment.registry, "addr", node)
	if err != nil {
		return unavailable()
	}
	if signer[0].(common.Address) != common.HexToAddress(p.signer) {
		return mismatch("Registry signer does not match the signed discovery documents.")
	}
	origin, err := call(deployment.registry, "text", node, "url")
	if err != nil {
		return unavailable()
	}
	if strings.TrimSuffix(origin[0].(string), "/") != p.origin {
		return mismatch("Registry URL does not match the inspected origin.")
	}
	reference, err := call(deployment.registry, "agentRef", node)
	if err != nil {
		return unavailable()
	}
	collection, token := reference[0].(common.Address), reference[1].(*big.Int)
	if collection == (common.Address{}) || token.Sign() <= 0 {
		return mismatch("Registry has no live NFT reference.")
	}
	known, err := call(deployment.manager, "isKnownCollection", collection)
	if err != nil {
		return unavailable()
	}
	if !known[0].(bool) {
		return mismatch("NFT collection is not registered with the official manager.")
	}
	collectionID, err := call(deployment.manager, "collectionIdByName", labels[2])
	if err != nil {
		return unavailable()
	}
	namedCollection, err := call(deployment.manager, "collections", collectionID[0])
	if err != nil {
		return unavailable()
	}
	if namedCollection[0].(common.Address) != collection {
		return mismatch("ENS collection label does not match its NFT collection.")
	}
	agentName, err := call(collection.Hex(), "nameOf", token)
	if err != nil {
		return unavailable()
	}
	if agentName[0].(string) != labels[1] {
		return mismatch("ENS agent label does not match its NFT.")
	}
	owner, err := call(collection.Hex(), "ownerOf", token)
	if err != nil {
		return unavailable()
	}
	if owner[0].(common.Address) == (common.Address{}) {
		return mismatch("NFT has no owner.")
	}
	controller, err := call(collection.Hex(), "addressToTokenId", "evm", strings.ToLower(p.signer))
	if err != nil {
		return unavailable()
	}
	if controller[0].(*big.Int).Cmp(token) != 0 {
		controller, err = call(collection.Hex(), "addressToTokenId", "evm", common.HexToAddress(p.signer).Hex())
		if err != nil {
			return unavailable()
		}
		if controller[0].(*big.Int).Cmp(token) != 0 {
			return mismatch("Discovery signer is not a controller of the registered NFT.")
		}
	}
	result.Verified, result.State, result.Collection, result.TokenID, result.Owner = true, "verified", collection.Hex(), token.String(), owner[0].(common.Address).Hex()
	return result
}
