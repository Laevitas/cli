# Browser terminal

Open https://terminal.laevitas.ch for production or https://terminal.devitas.ch
for development/test networks. Both run the actual released Go CLI compiled to
WebAssembly inside a browser Worker. There is no remote shell or shared process.

## Connect and use credits

Start with `agent sample` or `agent catalog`; neither needs a wallet. Select
**Connect wallet**, or run `agent login`. Base wallets use EIP-6963/EIP-1193;
Solana wallets use Wallet Standard. Mobile users open the site in their wallet's
browser. WalletConnect/QR pairing and smart-contract wallet signatures are not
supported by this first browser release.

The browser validates the API's exact sign-in message, wallet, website origin, network
and expiry before requesting a signature. Base wallets may first request a
network switch. Private keys stay in the wallet. This is authentication, not a
payment. Run `agent claim` to request starter credits under the existing live
eligibility and once-per-wallet rules, then `agent balance` or `agent usage`.

```text
agent sample
agent login
agent claim
agent balance
perps carry BTC-PERPETUAL -p 1h -n 1 -o json
agent logout
```

Supported: REST market-data groups, command discovery/help, browser doctor,
catalog/sample, wallet sessions, eligible claims, account/balance/usage,
quotes/status and Enterprise key management. `-o json`, `table` and `csv` use
the same Go output implementation. API keys still require Agent Enterprise.

Use the installed CLI for streaming/WebSockets, TUI dashboards, stdio MCP,
6022 partner calls, local configuration/files and payment signing. The browser
forces prepaid-credit authentication and stops at HTTP 402. It never imports
private keys, requests token approvals or signs payments. Browser quotes cannot
be transferred as a saved native checkout; create a native quote before paying
there. Existing wallets, credit balances and subscriptions are shared through
the existing APIs, not through local credential files.

## Browser agents

```js
const result = await window.laevitasTerminal.run({
  args: ["perps", "carry", "BTC-PERPETUAL", "-p", "1h", "-n", "1", "-o", "json"]
});
if (result.exitCode === 0 && result.json?.success) {
  console.log(result.json.data);
}
```

The interface returns `stdout`, `stderr`, `exitCode`, and `json` when stdout is
JSON. One command runs at a time. `capabilities` describes the runtime, API
origin and payment/storage policy. It does not expose tokens or wallet
providers. Login still needs the owner's wallet approval. Nothing executes
from URL parameters, shell operators or pasted scripts. Remote agents without
a browser should use the installed CLI or the REST/MCP APIs.

## Credential and update behavior

- Sessions, credit tokens, checkout tokens and command history live only in tab
  memory. Refresh, disconnect, expiry and wallet account/network changes clear
  them; disconnect also terminates the Worker. Explicit logout attempts server
  revocation. The server's 15-minute authentication expiry remains authoritative.
- No cookies are sent, no redirects are followed, and Go requests are restricted
  to `/api/v1/` on the fixed API origin selected by the site's hostname.
- Output is rendered as text, with terminal control codes removed and bounded
  history/output. Never paste secrets. Enterprise keys are displayed once only
  when explicitly created and should be saved privately by their owner.
- CSP restricts scripts/assets to this site and API connections to Laevitas.
  Wallet extensions and browser agents remain within the user's trust boundary.
- Each stable CLI release rebuilds and deploys the browser bundle after
  GoReleaser succeeds. The Go runtime and WASM hashes are checked at load and
  retained together for the life of the tab. An open tab reports new versions
  without changing its runtime; reload to use the update.

## Deployment

`.github/workflows/deploy-terminal.yml` deploys only the latest published stable
tag, through Pages project `laevitas-terminal`. It can be manually retried with
that tag after fixing infrastructure. The release still uses GoReleaser for all
six native archives, checksums, Homebrew and Scoop.

Secrets: `CF_API_TOKEN` and `CF_ACCOUNT_ID` (same inherited credentials as the
existing CLI and onboarding sites). Project deployment requires Pages Edit.
Domain setup additionally needs Zone Read and DNS Edit on `devitas.ch` and
`laevitas.ch`. `scripts/terminal-pages.mjs` changes only the two `terminal`
records, keeps their prior configuration on the ephemeral deployment runner, and binds
them to `laevitas-terminal.pages.dev`. It never changes the apex or Welcome.
DNS record contents are excluded from public workflow artifacts. The old dev
gateway and ingress are retained for rollback until routing has been verified;
its cluster ingress address can restore the previous dev route if needed.

The APIs must allow exact terminal origins in `CORS_ALLOWED_ORIGINS`, preserving
all existing entries: production `https://terminal.laevitas.ch`; development
`https://terminal.devitas.ch`. For the primary Pages deployment preview, allow
`https://laevitas-terminal.pages.dev` on production only; no wildcard previews.
The development API workflow already carries its terminal origin. Do not
change billing or eligibility settings when applying CORS configuration.

The agent auth service must support browser-origin challenges, and the API must
forward known terminal browser Origin on challenge and verify only. The signed domain and
`audience` match the terminal website; `apiOrigin` and the signed URI name the
API. The verifier allows only the exact environment-specific terminal origins,
requires the same Origin on verification, and consumes each nonce once. Native
clients and existing dashboard clients retain their API-domain sign-in flow. Deploy those auth
changes before this browser release; older challenges are refused before any
wallet signature is requested.

## Local validation

```sh
cd web-terminal
npm ci --ignore-scripts
npm test
npm run build
npx playwright install --with-deps chromium
npm run test:browser
node serve.mjs
```

Browser tests run actual WASM against mocked wallets/APIs and never sign real
authorizations or pay. Native regression coverage remains `go test ./...` and
`go vet ./...`. Deployment verification uses public sample/catalog, CORS
preflights, response headers and build metadata without connecting a wallet.
