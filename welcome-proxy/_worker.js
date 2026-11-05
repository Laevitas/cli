const UPSTREAM = 'https://velvet-otter-7392-prod-laevitas.up.railway.app';
const PUBLIC_ORIGIN = 'https://agents.laevitas.ch';
const SIGNER = '0x0e0ea97e037d079a36edeb37824c1be0e2ba0f4c';
const DISCOVERY = ['/.well-known/6022', '/.well-known/agent-card.json'];
const TURNS = ['/api/a2a', '/api/responses'];
const PUBLIC_LINKS = {
  '/': 'https://app.laevitas.ch/a2a',
  '/skill.md': 'https://apiv2.laevitas.ch/skill.md',
  '/.well-known/laevitas-agents.json': 'https://apiv2.laevitas.ch/api/v1/agents/catalog',
  '/.well-known/x402': 'https://apiv2.laevitas.ch/.well-known/x402',
};

function error(status, code, message) {
  return Response.json({ error: code, message, catalog: PUBLIC_LINKS['/.well-known/laevitas-agents.json'] },
    { status, headers: { 'cache-control': 'no-store', 'x-content-type-options': 'nosniff' } });
}
async function bounded(body, maximum) {
  if (!body) return new Uint8Array();
  const reader = body.getReader(), chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maximum) throw new Error('size');
      chunks.push(value);
    }
  } catch (e) { await reader.cancel(); throw e; }
  const bytes = new Uint8Array(size); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return bytes;
}
function expectedSignature(document) {
  // The proxy preserves bytes; clients verify detached JWS cryptographically.
  // This check only refuses accidental publication of another node's identity.
  return Array.isArray(document.signatures) && document.signatures.some(signature => {
    try {
      const protectedHeader = signature.protected.replace(/-/g, '+').replace(/_/g, '/');
      const header = JSON.parse(atob(protectedHeader));
      return header.alg === 'ES256K' && header.kid?.toLowerCase() === SIGNER && typeof signature.signature === 'string';
    } catch { return false; }
  });
}
function validPublication(documents) {
  const [discovery, card] = documents.map(bytes => JSON.parse(new TextDecoder().decode(bytes)));
  return discovery.protocol === '6022' && discovery.protocolVersion === '0.1' &&
    discovery.ensDomain === 'welcome-agent.laevitas-092026.137.6022.eth' && expectedSignature(discovery) && expectedSignature(card) &&
    discovery.endpoints?.responses === `${PUBLIC_ORIGIN}/api/responses` &&
    Array.isArray(card.supportedInterfaces) && card.supportedInterfaces.some(binding =>
      binding.protocolBinding === 'JSONRPC' && binding.protocolVersion === '1.0' && binding.url === `${PUBLIC_ORIGIN}/api/a2a`);
}

/** A narrow proxy for the existing Welcome runtime, never an agent implementation.
 * Signed URLs and payment terms are never rewritten. The operator must publish
 * signed discovery for PUBLIC_ORIGIN before the new origin becomes available. */
export function createWelcomeProxy(upstreamFetch = fetch) {
  const upstream = (path, options = {}) => upstreamFetch(`${UPSTREAM}${path}`, {
    ...options, redirect: 'manual', signal: AbortSignal.timeout(options.method === 'POST' ? 60000 : 10000),
  });
  return {
    async fetch(request) {
      const url = new URL(request.url), path = url.pathname;
      if (['GET', 'HEAD'].includes(request.method) && PUBLIC_LINKS[path]) return Response.redirect(PUBLIC_LINKS[path], 307);
      if (request.method !== (TURNS.includes(path) ? 'POST' : 'GET') || (!DISCOVERY.includes(path) && !TURNS.includes(path))) {
        return error(404, 'not_found', 'No agent endpoint is published at this path.');
      }
      if (url.search) return error(400, 'invalid_request', 'Query parameters are not supported.');
      let body;
      if (TURNS.includes(path)) {
        if (!/^application\/json(?:\s*;|$)/i.test(request.headers.get('content-type') || '')) return error(415, 'invalid_content_type', 'Send application/json.');
        try { body = await bounded(request.body, 64 * 1024); }
        catch { return error(413, 'request_too_large', 'Request exceeds 64 KiB.'); }
        try {
          const parsed = JSON.parse(new TextDecoder().decode(body));
          if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error();
          if (path === '/api/a2a' && (parsed.jsonrpc !== '2.0' || parsed.method !== 'SendMessage' ||
              !['string', 'number'].includes(typeof parsed.id) || parsed.params?.message?.role !== 'user' ||
              typeof parsed.params.message.messageId !== 'string' || !Array.isArray(parsed.params.message.parts))) throw new Error();
        } catch { return error(400, 'invalid_request', 'Send a valid JSON request. A2A supports SendMessage.'); }
      }
      let documents;
      try {
        documents = await Promise.all(DISCOVERY.map(async route => {
          const response = await upstream(route, { headers: { accept: 'application/json' } });
          if (response.status !== 200 || !response.headers.get('content-type')?.includes('application/json')) throw new Error();
          return bounded(response.body, 1024 * 1024);
        }));
        if (!validPublication(documents)) return error(503, 'agent_publication_pending', 'Welcome is not yet published at this address. Its operator must publish matching signed discovery.');
      } catch { return error(502, 'agent_unavailable', 'Welcome discovery is temporarily unavailable.'); }
      if (DISCOVERY.includes(path)) return new Response(documents[DISCOVERY.indexOf(path)], {
        headers: { 'content-type': 'application/json', 'cache-control': 'no-store', 'x-content-type-options': 'nosniff' },
      });
      const headers = new Headers({ 'content-type': 'application/json', accept: 'application/json' });
      // Never relay Laevitas sessions, API keys, credit tokens, cookies or arbitrary
      // forwarded headers. The partner payment protocol remains independent.
      const payment = request.headers.get('payment-signature');
      if (payment) {
        if (payment.length > 8192) return error(431, 'header_too_large', 'Payment header exceeds its size limit.');
        headers.set('payment-signature', payment);
      }
      try {
        const response = await upstream(path, { method: 'POST', headers, body });
        if (response.status >= 300 && response.status < 400) return error(502, 'agent_unavailable', 'Welcome returned an unexpected redirect.');
        const output = await bounded(response.body, 1024 * 1024);
        const responseHeaders = new Headers({ 'cache-control': 'no-store', 'x-content-type-options': 'nosniff' });
        for (const name of ['content-type', 'payment-required', 'payment-response', 'www-authenticate', 'retry-after']) {
          const value = response.headers.get(name); if (value) responseHeaders.set(name, value);
        }
        return new Response(output, { status: response.status, headers: responseHeaders });
      } catch { return error(502, 'agent_unavailable', 'Welcome is temporarily unavailable.'); }
    },
  };
}

export default createWelcomeProxy();
