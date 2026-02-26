package x402

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	x402sdk "github.com/coinbase/x402/go"
	x402http "github.com/coinbase/x402/go/http"
	evmclient "github.com/coinbase/x402/go/mechanisms/evm/exact/client"
	evmsigner "github.com/coinbase/x402/go/signers/evm"
)

// PaymentClient wraps the coinbase x402 SDK for creating signed payments.
type PaymentClient struct {
	x402Client *x402sdk.X402Client
	httpClient *x402http.HTTPClient
	address    string // wallet address derived from private key
}

// NewPaymentClient creates a payment client from a hex-encoded EVM private key.
func NewPaymentClient(privateKeyHex string) (*PaymentClient, error) {
	signer, err := evmsigner.NewClientSignerFromPrivateKey(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid wallet key: %w", err)
	}

	client := x402sdk.Newx402Client()
	client.Register("eip155:*", evmclient.NewExactEvmScheme(signer))

	httpClient := x402http.NewClient(client)

	return &PaymentClient{
		x402Client: client,
		httpClient: httpClient,
		address:    signer.Address(),
	}, nil
}

// Address returns the EVM wallet address derived from the private key.
func (pc *PaymentClient) Address() string {
	return pc.address
}

// HandlePaymentRequired parses a 402 response and creates a signed payment.
// Returns the headers to include on the retry request.
func (pc *PaymentClient) HandlePaymentRequired(resp *http.Response, body []byte) (map[string]string, error) {
	// Extract headers from response
	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	// Parse payment requirements from 402 response
	paymentRequired, err := pc.httpClient.GetPaymentRequiredResponse(headers, body)
	if err != nil {
		return nil, fmt.Errorf("parsing payment requirements: %w", err)
	}

	// Select a payment scheme we can fulfill
	selected, err := pc.x402Client.SelectPaymentRequirements(paymentRequired.Accepts)
	if err != nil {
		return nil, fmt.Errorf("no supported payment scheme: %w", err)
	}

	// Create and sign the payment payload
	ctx := context.Background()
	payload, err := pc.x402Client.CreatePaymentPayload(
		ctx,
		selected,
		paymentRequired.Resource,
		paymentRequired.Extensions,
	)
	if err != nil {
		return nil, fmt.Errorf("creating payment: %w", err)
	}

	// Encode to header format
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding payment: %w", err)
	}

	paymentHeaders, err := pc.httpClient.EncodePaymentSignatureHeader(payloadBytes)
	if err != nil {
		return nil, fmt.Errorf("encoding payment header: %w", err)
	}

	return paymentHeaders, nil
}
