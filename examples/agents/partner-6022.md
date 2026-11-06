# Connect to Laevitas Welcome with 6022 and A2A

[agents.laevitas.ch](https://agents.laevitas.ch) is the onboarding website.
Your agent talks directly to Welcome at
`https://velvet-otter-7392-prod-laevitas.up.railway.app`.
Welcome stays there; publishing signed discovery under a Laevitas domain is not
required. Its two public discovery paths now redirect with HTTP 307 to Welcome's
signed Railway documents, so agents can start discovery at the Laevitas domain.
No factory API key or 6022 mint is needed.

## Connection information

```json
{
  "name": "Laevitas Welcome",
  "description": "Onboarding and dedicated agent requests.",
  "origin": "https://velvet-otter-7392-prod-laevitas.up.railway.app",
  "ens": "welcome-agent.laevitas-092026.137.6022.eth",
  "expectedAgentWallet": "0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C",
  "agentCard": "https://velvet-otter-7392-prod-laevitas.up.railway.app/.well-known/agent-card.json",
  "discovery6022": "https://velvet-otter-7392-prod-laevitas.up.railway.app/.well-known/6022",
  "a2a": {
    "url": "https://velvet-otter-7392-prod-laevitas.up.railway.app/api/a2a",
    "protocolBinding": "JSONRPC",
    "protocolVersion": "1.0",
    "method": "SendMessage",
    "streaming": false
  },
  "access": {
    "protocol": "x402",
    "version": 2,
    "network": "eip155:137",
    "requireZeroAmount": true,
    "signatureHeader": "PAYMENT-SIGNATURE",
    "challengeHeader": "PAYMENT-REQUIRED",
    "fetchFreshChallenge": true
  },
  "optionalSkill": "https://github.com/6022-labs/agentic-skills/tree/main/skills/call-agent-a2a"
}
```

## Verify discovery with the Laevitas CLI

```sh
# v0.12.3+: start at the Laevitas discovery entry point, with the pinned signer:
laevitas agent partner inspect --origin https://agents.laevitas.ch --signer 0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C
# Sends an unsigned request; stops at HTTP 402 without signing or paying:
laevitas agent partner ask --origin https://agents.laevitas.ch --signer 0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C --message "Show me how to discover Laevitas products"
# For an endpoint that has returned a successful turn, continue its context:
laevitas agent partner ask --origin https://agents.laevitas.ch --signer 0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C --context-id CONTEXT --message "What is free to try?"
```

The original Railway `--origin` also remains supported, including by older CLI
versions. The connection JSON above keeps the runtime's canonical URLs intact.

The CLI verifies both signed documents with RFC 8785/JCS canonicalization and
ES256K JWS using SHA-256 against the supplied expected signer. It reads the A2A
1.0 JSONRPC URL from `supportedInterfaces` and sends `SendMessage` there. It
allows one exact HTTP 307 discovery hop from `agents.laevitas.ch` to the Railway
origin only when `--signer` is supplied. Both documents must resolve to that same
origin. Inspection reports `discoveryOrigin` separately from canonical `origin`
and `endpoint`. It refuses other redirects, A2A POST redirects, changed signers,
cross-origin interfaces and invalid/error replies. Laevitas credentials are never
sent to the partner.

`inspect` also checks the canonical 6022 registry and NFT binding on Polygon or
Amoy at one block. `registryVerified` is true only when all checks match;
`registry.state` distinguishes `verified`, `mismatch`, `unavailable` and
`not_checked`. Use `--registry-rpc HTTPS_URL` for a trusted RPC on that chain.
This is separate from document signature verification and Ethereum ENS/CCIP
resolution (`ensResolutionVerified:false`). `liveExchangeVerified:false` means
inspection has not established a conversation, payment, provisioning or activation.

On HTTP 402, `partner ask` reports `delivered:false` and the current payment
requirements. **It does not sign Welcome's challenge, even when the amount is
zero.** Use an x402 v2 EVM signing client for that step. An A2A client alone is
not sufficient. Laevitas REST/MCP credits, starter claims and subscriptions are
separate; Welcome access does not buy credits or activate a Laevitas subscription.

## Signature-required free access

1. Fetch the signed Agent Card and 6022 discovery document. Verify both
   ES256K/JCS signatures against `expectedAgentWallet` above. ENS is an additional
   discovery route; installing 6022 skills or minting your own agent is not required.
2. Use an A2A JSON-RPC client compatible with the advertised protocol version and
   an x402 v2 EVM signer. Keep private keys local; never send a key to Welcome or
   include it in a message.
3. POST the `SendMessage` JSON below to the verified HTTPS `/api/a2a` URL with
   `Content-Type: application/json`. Generate fresh request and message UUIDs for
   each new message. Use POST, not GET.
4. On HTTP 402, read fresh requirements from the base64-encoded JSON in
   `PAYMENT-REQUIRED`, or the JSON response body. Select a supported `exact` offer
   on Polygon (`eip155:137`) with amount exactly the string `"0"`. If no such offer
   exists, stop and ask the organization for assistance. Never accept a paid offer
   automatically, including when using a generic x402 client or retry helper.
5. Sign that zero-amount offer locally using its advertised `assetTransferMethod`
   (Permit2 or EIP-3009) and current parameters. Do not invent token metadata,
   recipient, amount or signature format. Encode the x402 v2 payload as required
   and send it in `PAYMENT-SIGNATURE`.
6. Retry the same A2A request at the same verified HTTPS endpoint. If the
   challenge's `resource.url` shows internal HTTP or `/a2a` without `/api`, keep the
   HTTPS endpoint from the signed Agent Card. Never downgrade to HTTP or forward
   a signature to another host.
7. Read the A2A result or error. Free access requires a signature, but does not
   settle a token payment or require an on-chain approval transaction. Discovery
   alone is not proof of successful payment, provisioning or activation.
8. For later messages in the same conversation, reuse Welcome's returned
   `contextId` in `params.message.contextId`. A context ID is not wallet ownership
   or authorization to access a customer record.
9. Introduce the customer's use case and preferred language. Welcome can explain
   services and coordinate a dedicated agent. Paid subscriptions, activation and
   optional sponsored 6022 minting are separate workflows with their own terms
   and authorization.

## First message

```json
{
  "jsonrpc": "2.0",
  "id": "REPLACE_WITH_NEW_REQUEST_UUID",
  "method": "SendMessage",
  "params": {
    "message": {
      "messageId": "REPLACE_WITH_NEW_MESSAGE_UUID",
      "role": "user",
      "parts": [
        {
          "text": "Hello Welcome. I represent a client interested in your services. How can we get started?"
        }
      ]
    }
  }
}
```

## Optional client setup

If your client lacks the signing flow, review the official
[6022 call-agent-a2a skill](https://github.com/6022-labs/agentic-skills/tree/main/skills/call-agent-a2a),
install its dependencies in your own environment and configure your signer
locally. Ensure the client enforces the zero-amount Polygon policy before signing;
a generic automatic x402 retry must not accept a paid offer for Welcome.
Welcome advertises public access through HTTP 402. A private dedicated agent may
later require a separate connection package supplied by Welcome. Never request
or use a factory MCP key to contact Welcome.

Deployment verification uses public discovery and an unsigned challenge only.
Do not sign wallet authorizations or submit payments as a smoke test.
