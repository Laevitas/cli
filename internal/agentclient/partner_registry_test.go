package agentclient

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func TestPartnerRegistryRequiresMatchingNameSignerURLAndNFT(t *testing.T) {
	for _, mode := range []string{"verified", "chain", "signer", "origin", "unprovisioned", "collection", "name", "owner", "controller", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			parsed, _ := abi.JSON(strings.NewReader(registryABI))
			collection := common.HexToAddress("0x1234567890123456789012345678901234567890")
			signer := common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd")
			origin := "https://agents.example.test"
			name := "welcome.laevitas.137.6022.eth"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Payment-Signature") != "" {
					t.Error("credential leaked")
				}
				var request struct {
					ID     int               `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				json.NewDecoder(r.Body).Decode(&request)
				if mode == "rpc" {
					w.WriteHeader(503)
					return
				}
				result := ""
				switch request.Method {
				case "eth_chainId":
					result = "0x89"
					if mode == "chain" {
						result = "0x1"
					}
				case "eth_blockNumber":
					result = "0x123456"
				case "eth_call":
					var call struct {
						To   string `json:"to"`
						Data string `json:"data"`
					}
					json.Unmarshal(request.Params[0], &call)
					var block string
					json.Unmarshal(request.Params[1], &block)
					if block != "0x123456" {
						t.Error("registry reads must share one block")
					}
					data, _ := hex.DecodeString(strings.TrimPrefix(call.Data, "0x"))
					method, err := parsed.MethodById(data[:4])
					if err != nil {
						t.Fatal(err)
					}
					args, err := method.Inputs.Unpack(data[4:])
					if err != nil {
						t.Fatal(err)
					}
					values := []any{}
					switch method.Name {
					case "manager":
						values = []any{common.HexToAddress(registryDeployments["137"].manager)}
					case "provisioned":
						if args[0].([32]byte) != nameHash(name) {
							t.Error("wrong ENS node")
						}
						values = []any{mode != "unprovisioned"}
					case "addr":
						address := signer
						if mode == "signer" {
							address = common.Address{}
						}
						values = []any{address}
					case "text":
						url := origin
						if mode == "origin" {
							url = "https://wrong.example.test"
						}
						values = []any{url}
					case "agentRef":
						values = []any{collection, big.NewInt(7)}
					case "isKnownCollection":
						values = []any{mode != "collection"}
					case "collectionIdByName":
						if args[0] != "laevitas" {
							t.Error("wrong collection name")
						}
						values = []any{big.NewInt(2)}
					case "collections":
						values = []any{collection}
					case "nameOf":
						label := "welcome"
						if mode == "name" {
							label = "other"
						}
						values = []any{label}
					case "ownerOf":
						address := signer
						if mode == "owner" {
							address = common.Address{}
						}
						values = []any{address}
					case "addressToTokenId":
						token := int64(7)
						if mode == "controller" {
							token = 9
						}
						values = []any{big.NewInt(token)}
					default:
						t.Error("unexpected contract call", method.Name)
					}
					output, err := method.Outputs.Pack(values...)
					if err != nil {
						t.Fatal(err)
					}
					result = "0x" + hex.EncodeToString(output)
				default:
					t.Error("unexpected or mutating RPC method", request.Method)
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			p := &partner{origin: origin, signer: signer.Hex(), discovery: map[string]any{"ensDomain": name}}
			result := verifyPartnerRegistry(context.Background(), p, server.URL)
			if result.Verified != (mode == "verified") {
				t.Fatalf("incorrect verification: %+v", result)
			}
			if result.ENSResolutionVerified {
				t.Fatal("direct registry reads must not claim L1 ENS resolution")
			}
			if mode == "verified" && (result.TokenID != "7" || result.Collection != collection.Hex() || result.Block != "0x123456") {
				t.Fatal(result)
			}
			if mode == "rpc" && result.State != "unavailable" {
				t.Fatal(result)
			}
			if mode != "verified" && mode != "rpc" && result.State != "mismatch" {
				t.Fatal(result)
			}
		})
	}
}

func TestPartnerRegistryDoesNotUseUntrustedEndpointsOrUnknownNames(t *testing.T) {
	for _, name := range []string{"", "evil.example", "agent.collection.1.6022.eth", "A.collection.137.6022.eth"} {
		p := &partner{discovery: map[string]any{"ensDomain": name, "rpc": "https://attacker.example"}}
		result := verifyPartnerRegistry(context.Background(), p, "")
		if result.Verified || result.State != "not_checked" {
			t.Fatal(result)
		}
	}
	p := &partner{discovery: map[string]any{"ensDomain": "welcome.laevitas.137.6022.eth"}}
	for _, endpoint := range []string{"http://rpc.example", "https://user:secret@rpc.example", "file:///tmp/rpc"} {
		result := verifyPartnerRegistry(context.Background(), p, endpoint)
		if result.Verified || result.State != "unavailable" || strings.Contains(result.Reason, "secret") {
			t.Fatal(result)
		}
	}
}
