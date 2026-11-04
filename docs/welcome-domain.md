# Welcome on agents.laevitas.ch

This origin proxies the existing 6022 Welcome runtime. It does not run a separate
agent, mint identities, change ownership, or administer the partner's services.
Only the two signed discovery documents and `/api/a2a` and `/api/responses` are
proxied. Admin, console and health routes return 404. Laevitas credentials and
cookies are never forwarded. Payment protocol headers are preserved verbatim.

Deployment uses the `laevitas-agents` Cloudflare Pages project, independently of
the CLI website. Attach the custom domain `agents.laevitas.ch` to this project.
Run `node --test test/welcome-proxy.test.mjs` before deploying.

Before publication, the 6022 operator must:

1. Configure Welcome's canonical public origin as `https://agents.laevitas.ch`.
2. Re-sign `/.well-known/6022` and `/.well-known/agent-card.json` with its existing
   registered signer. Publish `/api/responses` in the 6022 endpoints and
   `/api/a2a` in the A2A supported interface. Never edit signed bytes at the proxy.
3. Update the existing ENS registry `url` record to the same origin.
4. Remove Welcome's access rules if access is intended to require no wallet
   authorization. A zero-price rule still requires a signature. Any remaining
   payment challenge needs an HTTPS resource matching the actual endpoint and
   a positive timeout; the proxy does not change payment terms.

Until signed discovery advertises this origin, the proxy returns HTTP 503 with
`agent_publication_pending`. This is intentionally not a fabricated agent card.
After publication, run `laevitas agent partner inspect --origin
https://agents.laevitas.ch --signer 0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C`
and check signatures and registry verification. Then test a no-payment nonce echo
and context continuation with `partner ask`; stop on 402. Only a real successful
exchange establishes conversational readiness.

The catalog and wallet starter claims remain on `apiv2.laevitas.ch`, independent
of partner access rules. The proxy redirects `/skill.md`,
`/.well-known/laevitas-agents.json` and `/.well-known/x402` to those public resources.
