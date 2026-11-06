# Agent commands

Agent account commands emit `{success,data,meta}` or `{success:false,error,meta}`.
`agent mcp` uses MCP JSONRPC over stdio instead. It is a persistent connection.

| Command | Purpose / flags |
|---|---|
| `agent catalog` / `agent sample` | Free discovery / fixed fictional data |
| `wallet init --generate` | New dedicated local wallet; no key printed or overwrite |
| `agent login` / `agent logout` | Sign in / revoke this management session |
| `agent account` / `agent balance` | Current paid entitlement / remaining credits |
| `agent usage --before CURSOR` | Credit entries; continue with `nextBefore` |
| `agent quote PRODUCT --request-id UUID --cycle MONTHLY` | Retry-safe quote; cycle only for Enterprise; no payment |
| `agent pay CHECKOUT_ID --max-usdc LIMIT` | Explicit capped payment for a saved quote |
| `agent status CHECKOUT_ID` | Poll an interrupted or pending purchase |
| `agent keys list` | Labels, expiries and revocations |
| `agent keys create --label NAME` | Enterprise only; plaintext `data.key` shown once |
| `agent keys revoke KEY_ID` | Revoke a key owned by this wallet |
| `agent mcp` | MCP over stdio; existing credits or configured Enterprise key |
| `agent partner inspect --origin URL --signer ADDRESS` | Verify signed 6022 documents; no payment or live turn |
| `agent partner ask --origin URL --signer ADDRESS --message TEXT --context-id ID` | A2A SendMessage; report a payment challenge without signing |
| `wallet credits` | Live balance, not a cached token estimate |

Billing periods: `MONTHLY`, `QUARTERLY`, `BIYEARLY`, `YEARLY`. Product IDs and
prices come from the current catalog. `--max-usdc` is required and applies per
purchase. Agent Enterprise is prepaid, 25% below the same current Enterprise
period, without dashboard seats or automatic renewal. Credits do not grant keys.

`LAEVITAS_AUTH=credits` spends existing wallet credits and never buys more
implicitly. Explicit `x402` preserves automatic legacy purchases.
`LAEVITAS_CONFIG_DIR` sets a private configuration directory for an agent;
`LAEVITAS_BASE_URL` changes the API origin. Credentials never cross wallet or
origin boundaries. The CLI uses Base EOA wallets; HTTP also supports Solana.

See [working setup examples](../../../examples/agents/README.md),
[stdio MCP configuration](../../../examples/agents/mcp.json), and
[6022 verification boundaries](../../../examples/agents/partner-6022.md).

### Welcome partner access

`agents.laevitas.ch` is the onboarding website. Use the Railway origin and pinned
expected signer in the [Welcome instructions](../../../examples/agents/partner-6022.md).
Both discovery signatures must verify. Free access requires an x402 v2 signature
for a supported `exact` offer with amount `"0"` on Polygon (`eip155:137`).
`partner ask` stops at 402 without signing; use a compatible local signer that
rejects paid offers. No factory API key, own-domain publication or 6022 mint is
required. This does not change Laevitas REST/MCP billing or starter eligibility.

### Free starter credits

Run `laevitas update` and use your existing locally configured wallet. A newly generated empty wallet does not qualify for free live-data credits; `laevitas agent sample` remains free.
Run `laevitas agent login` and `laevitas agent claim`. The claim requires a verified wallet signature, accepts no wallet or amount arguments, and never signs a payment.
The live `agent catalog` publishes `starterOffer.eligibility` and `starterOffer.limits` (1,000 credits at launch). Eligibility requires three outgoing transactions confirmed at least seven days ago on Base/Solana mainnet, or a completed agent purchase of at least $1. Daily IP and global limits apply. One grant per wallet across sessions and networks; a duplicate returns HTTP 200 with `alreadyClaimed:true` and the original `creditsGranted`, adding no credits. Missing sign-in returns 401, ineligibility 403, limits 429, and unavailable eligibility checks 503. Credits have no expiry. Never buy a pack automatically to unlock a claim.
Use `agent balance` and `agent usage` to inspect the balance and starter claim. Claimed credits cover live REST and MCP. Connect `laevitas agent mcp` in your MCP client; it spends existing credits without buying more automatically.
When credits run out, request a credit-pack quote and approve its price explicitly. API keys still require paid Agent Enterprise.
Browser onboarding: https://app.laevitas.ch/claim.
