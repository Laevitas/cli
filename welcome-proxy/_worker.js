const WELCOME_ORIGIN = 'https://velvet-otter-7392-prod-laevitas.up.railway.app';
const PUBLIC_LINKS = {
  '/': 'https://app.laevitas.ch/a2a',
  '/skill.md': 'https://apiv2.laevitas.ch/skill.md',
  '/.well-known/laevitas-agents.json': 'https://apiv2.laevitas.ch/api/v1/agents/catalog',
  '/.well-known/x402': 'https://apiv2.laevitas.ch/.well-known/x402',
};

/** This domain is an onboarding website. Welcome stays at its signed Railway
 * origin. Never proxy authorizations, redirect POST bodies or publish a card
 * claiming that this website is the agent. */
export default {
  async fetch(request) {
    const path = new URL(request.url).pathname;
    const headers = { 'cache-control': 'no-store', 'x-content-type-options': 'nosniff' };
    if (['GET', 'HEAD'].includes(request.method) && PUBLIC_LINKS[path]) {
      return new Response(null, { status: 307, headers: { ...headers, location: PUBLIC_LINKS[path] } });
    }
    const body = {
      error: 'not_found',
      message: 'This is the Laevitas onboarding website. No A2A agent is hosted here. Connect directly to Welcome at its Railway origin.',
      onboarding: PUBLIC_LINKS['/'],
      catalog: PUBLIC_LINKS['/.well-known/laevitas-agents.json'],
      welcome: {
        origin: WELCOME_ORIGIN,
        agentCard: `${WELCOME_ORIGIN}/.well-known/agent-card.json`,
        discovery6022: `${WELCOME_ORIGIN}/.well-known/6022`,
        a2a: `${WELCOME_ORIGIN}/api/a2a`,
      },
    };
    return new Response(request.method === 'HEAD' ? null : JSON.stringify(body), {
      status: 404, headers: { ...headers, 'content-type': 'application/json' },
    });
  },
};
