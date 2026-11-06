# Welcome onboarding at agents.laevitas.ch

`agents.laevitas.ch` is the onboarding website, not the Welcome node. Its
homepage (`GET` or `HEAD /`) redirects to `https://app.laevitas.ch/a2a` without
forwarding query parameters. Welcome remains at its existing Railway origin:

- Origin: https://velvet-otter-7392-prod-laevitas.up.railway.app
- Signed Agent Card: https://velvet-otter-7392-prod-laevitas.up.railway.app/.well-known/agent-card.json
- Signed 6022 discovery: https://velvet-otter-7392-prod-laevitas.up.railway.app/.well-known/6022
- A2A: HTTPS POST https://velvet-otter-7392-prod-laevitas.up.railway.app/api/a2a
- Expected signer: `0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C`

Agents can discover Welcome from the Laevitas domain: `GET` and `HEAD` on
`/.well-known/agent-card.json` and `/.well-known/6022` return HTTP 307 to the
corresponding signed documents above. Redirects use fixed destinations, discard
query parameters and are not cached. POST requests and conversation paths still
return JSON 404 responses with links to Welcome. The website never fabricates
signed documents, proxies messages or forwards wallet authorizations.

CLI v0.12.3 and later accept `--origin https://agents.laevitas.ch` with the pinned
`--signer` above. They follow only these exact discovery redirects and verify both
signatures. `discoveryOrigin` records the Laevitas entry point; `origin` and
`endpoint` identify the signed Railway runtime. Other redirects, including A2A
POST redirects, remain forbidden.

No ENS update or runtime migration is needed for these discovery links. Keeping
Railway out of the advertised runtime URLs is a separate migration: configure
Welcome's canonical public HTTPS origin at the 6022 runtime, regenerate signed
discovery, and align routing and registry identity. Rewriting endpoint strings in
the existing signed JSON would invalidate its signatures.

Welcome offers **signature-required free access** via x402 v2 on Polygon
(`eip155:137`). Only accept a supported `exact` offer whose amount is the string
`"0"`; never automatically accept a paid offer. See the complete
[connection and signing instructions](../examples/agents/partner-6022.md).

The existing `welcome-proxy/` directory now contains the onboarding website
router. Deployment remains independent of CLI binary releases: the
`deploy-welcome.yml` workflow publishes to the `laevitas-agents` Cloudflare Pages
project with custom domain `agents.laevitas.ch`. Run
`node --test test/welcome-proxy.test.mjs` before deployment, then verify homepage
and discovery GET/HEAD redirects, valid destination signatures and POST refusal.
Test public discovery and an unsigned A2A challenge only; do not sign or submit
payments as a deployment check.

Laevitas REST/MCP billing, starter credits and subscriptions remain separate.
`/skill.md`, `/.well-known/laevitas-agents.json` and `/.well-known/x402` continue
redirecting to the existing API resources on `apiv2.laevitas.ch`.
