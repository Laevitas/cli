# 6022 discovery and A2A

```sh
laevitas agent partner inspect --origin https://YOUR_NODE --signer VERIFIED_EVM_SIGNER
laevitas agent partner ask --origin https://YOUR_NODE --signer VERIFIED_EVM_SIGNER --message "Show me how to discover Laevitas products"
# Continue with the contextId returned by a successful turn:
laevitas agent partner ask --origin https://YOUR_NODE --signer VERIFIED_EVM_SIGNER --context-id CONTEXT --message "What is free to try?"
```

The client verifies both `/.well-known/6022` (0.1) and
`/.well-known/agent-card.json` with RFC 8785 JSON canonicalization and detached
ES256K JWS using SHA-256. It requires the same signer, reads the A2A 1.0 JSONRPC
URL from `supportedInterfaces`, and sends `SendMessage` there. It refuses
redirects, changed signers, cross-origin interfaces and invalid/error replies.
Laevitas credentials are never sent to the partner.

A valid signature proves document integrity. It does **not** prove ENS/NFT
ownership. Verify that binding with the partner's official tools and supply the
verified signer. `inspect` explicitly reports `registryVerified: false` and
`liveExchangeVerified: false`; a real nonce-echo exchange is a separate test.

On HTTP 402, `ask` reports `delivered: false` and the payment requirements. It
never signs or pays a partner challenge. A zero-priced payment rule can still
require wallet authorization. A completely unsigned welcome flow requires the
node owner to configure no payment rule. Mainnet minting, ENS writes, rule
changes and payments require the owner's separate approval.

This CLI implements the public protocol. It does not provision a node, hold a
factory signer, promise a free welcome turn, or act as a payment facilitator.
Use the partner's official client for any approved payment authorization.

Reference: [6022 agentic skills](https://github.com/6022-labs/agentic-skills/tree/886a9be174f31f1fff50f61fa3494e9edc6598ef),
reviewed at the pinned revision. The local test suite covers signed discovery,
wrong-hash/tamper rejection, advertised routing, successful replies, JSONRPC
errors and payment-required responses. A paid live partner exchange is not part
of those tests.
