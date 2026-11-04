import test from 'node:test';
import assert from 'node:assert/strict';
import { createWelcomeProxy } from '../welcome-proxy/_worker.js';

const origin = 'https://agents.laevitas.ch';
const signatures = [{ protected: Buffer.from(JSON.stringify({ alg: 'ES256K', kid: '0x0E0ea97e037d079a36EDeb37824c1bE0E2BA0F4C' })).toString('base64url'), signature: 'fixture-only' }];
const discovery = { protocol: '6022', protocolVersion: '0.1', ensDomain: 'welcome-agent.laevitas-092026.137.6022.eth', endpoints: { responses: `${origin}/api/responses` }, signatures };
const card = { supportedInterfaces: [{ protocolBinding: 'JSONRPC', protocolVersion: '1.0', url: `${origin}/api/a2a` }], signatures };
const turn = { jsonrpc: '2.0', id: 'rpc-1', method: 'SendMessage', params: { message: { messageId: 'message-1', role: 'user', contextId: 'context-1', parts: [{ text: 'nonce-7' }] } } };
const request = (path = '/api/a2a', headers = {}, body = JSON.stringify(turn)) => new Request(origin + path, { method: 'POST', headers: { 'content-type': 'application/json', ...headers }, body });

test('preserves signed discovery bytes without rewriting URLs or signatures', async () => {
  const raw = JSON.stringify(discovery, null, 2);
  const proxy = createWelcomeProxy(async url => new Response(url.endsWith('/6022') ? raw : JSON.stringify(card), { headers: { 'content-type': 'application/json' } }));
  const response = await proxy.fetch(new Request(`${origin}/.well-known/6022`));
  assert.equal(response.status, 200); assert.equal(await response.text(), raw);
});
test('keeps publication closed while the operator advertises its old origin', async () => {
  let turns = 0;
  const proxy = createWelcomeProxy(async url => {
    if (url.endsWith('/api/a2a')) turns++;
    return Response.json(url.endsWith('/6022') ? discovery : { ...card, supportedInterfaces: [{ ...card.supportedInterfaces[0], url: 'https://old.example/api/a2a' }] });
  });
  for (const req of [new Request(`${origin}/.well-known/agent-card.json`), request()]) {
    const response = await proxy.fetch(req); assert.equal(response.status, 503);
    assert.equal((await response.json()).error, 'agent_publication_pending');
  }
  assert.equal(turns, 0);
});
test('relays the actual turn and payment headers while stripping Laevitas credentials', async () => {
  const required = Buffer.from(JSON.stringify({ x402Version: 2, accepts: [] })).toString('base64');
  const proxy = createWelcomeProxy(async (url, options) => {
    assert.equal(options.redirect, 'manual');
    if (url.endsWith('/6022')) return Response.json(discovery);
    if (url.endsWith('/agent-card.json')) return Response.json(card);
    assert.equal(url, 'https://velvet-otter-7392-prod-laevitas.up.railway.app/api/a2a');
    const headers = new Headers(options.headers);
    for (const name of ['authorization', 'x-api-key', 'x-credit-token', 'cookie', 'x-forwarded-host']) assert.equal(headers.get(name), null);
    assert.equal(headers.get('payment-signature'), 'user-approved-partner-authorization');
    assert.deepEqual(JSON.parse(new TextDecoder().decode(options.body)), turn);
    return Response.json({ x402Version: 2, accepts: [] }, { status: 402, headers: { 'payment-required': required, 'payment-response': 'receipt', 'set-cookie': 'private', 'x-debug': 'private' } });
  });
  const response = await proxy.fetch(request('/api/a2a', { authorization: 'Bearer lva_session_secret', 'x-api-key': 'key', 'x-credit-token': 'credit', cookie: 'session', 'x-forwarded-host': 'evil.example', 'payment-signature': 'user-approved-partner-authorization' }));
  assert.equal(response.status, 402); assert.equal(response.headers.get('payment-required'), required);
  assert.equal(response.headers.get('payment-response'), 'receipt'); assert.equal(response.headers.get('set-cookie'), null); assert.equal(response.headers.get('x-debug'), null);
});
test('never exposes administration or follows redirects', async () => {
  let calls = 0;
  const proxy = createWelcomeProxy(async () => { calls++; return Response.redirect('https://evil.example', 302); });
  for (const path of ['/admin', '/health', '/console', '/api/a2a/../admin', '/anything']) assert.equal((await proxy.fetch(new Request(origin + path))).status, 404);
  assert.equal(calls, 0);
  assert.equal((await proxy.fetch(new Request(`${origin}/.well-known/6022`))).status, 502);
  assert.equal(calls, 2);
});
test('rejects oversized and malformed requests before calling the partner', async () => {
  const proxy = createWelcomeProxy(() => { throw new Error('must not call'); });
  assert.equal((await proxy.fetch(request('/api/a2a', {}, 'x'.repeat(65537)))).status, 413);
  assert.equal((await proxy.fetch(request('/api/a2a', {}, '{}'))).status, 400);
  assert.equal((await proxy.fetch(request('/api/a2a', { 'content-type': 'text/plain' }))).status, 415);
  assert.equal((await proxy.fetch(request('/api/a2a?origin=https://evil.example'))).status, 400);
});
test('agents can discover the wallet claim catalog and skill without partner readiness', async () => {
  const proxy = createWelcomeProxy(() => { throw new Error('must not call'); });
  for (const path of ['/skill.md', '/.well-known/laevitas-agents.json', '/.well-known/x402']) {
    const response = await proxy.fetch(new Request(origin + path));
    assert.equal(response.status, 307); assert.equal(new URL(response.headers.get('location')).origin, 'https://apiv2.laevitas.ch');
  }
});
