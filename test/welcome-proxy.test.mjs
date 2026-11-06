import test from 'node:test';
import assert from 'node:assert/strict';
import website from '../welcome-proxy/_worker.js';

const origin = 'https://agents.laevitas.ch';
const welcome = 'https://velvet-otter-7392-prod-laevitas.up.railway.app';

test('homepage and Laevitas discovery stay available without contacting Welcome', async t => {
  t.mock.method(globalThis, 'fetch', () => { throw new Error('website must not call upstream'); });
  const links = {
    '/': 'https://app.laevitas.ch/a2a',
    '/skill.md': 'https://apiv2.laevitas.ch/skill.md',
    '/.well-known/laevitas-agents.json': 'https://apiv2.laevitas.ch/api/v1/agents/catalog',
    '/.well-known/x402': 'https://apiv2.laevitas.ch/.well-known/x402',
  };
  for (const [path, target] of Object.entries(links)) {
    for (const method of ['GET', 'HEAD']) {
      const response = await website.fetch(new Request(`${origin}${path}?next=https://untrusted.example&token=not-forwarded`, { method }));
      assert.equal(response.status, 307);
      assert.equal(response.headers.get('location'), target);
      assert.equal(response.headers.get('cache-control'), 'no-store');
      assert.equal(await response.text(), '');
    }
  }
  assert.equal(globalThis.fetch.mock.callCount(), 0);
});

test('public discovery redirects GET and HEAD to the exact signed documents', async t => {
  t.mock.method(globalThis, 'fetch', () => { throw new Error('website must not call upstream'); });
  for (const path of ['/.well-known/6022', '/.well-known/agent-card.json']) {
    for (const method of ['GET', 'HEAD']) {
      const response = await website.fetch(new Request(origin + path + '?next=https://untrusted.example&token=not-forwarded', { method }));
      assert.equal(response.status, 307);
      assert.equal(response.headers.get('location'), welcome + path);
      assert.equal(response.headers.get('cache-control'), 'no-store');
      assert.equal(await response.text(), '');
    }
  }
  assert.equal(globalThis.fetch.mock.callCount(), 0);
});

test('conversation paths remain explicit 404s, with no fabricated agent identity', async () => {
  for (const path of ['/api/a2a', '/api/responses']) {
    const response = await website.fetch(new Request(origin + path));
    assert.equal(response.status, 404);
    const body = await response.json();
    assert.equal(body.error, 'not_found');
    assert.equal(body.welcome.a2a, `${welcome}/api/a2a`);
    assert.equal(body.signatures, undefined);
    assert.equal(body.supportedInterfaces, undefined);
  }
});

test('never forwards messages, signatures or credentials from the website', async t => {
  t.mock.method(globalThis, 'fetch', () => { throw new Error('website must not call upstream'); });
  for (const path of ['/', '/skill.md', '/.well-known/agent-card.json', '/.well-known/6022', '/api/a2a', '/a2a', '/api/responses', '/admin', '/console', '/health']) {
    const response = await website.fetch(new Request(origin + path, {
      method: 'POST', body: '{"message":"local-test-only"}', headers: {
        'content-type': 'application/json', 'payment-signature': 'fixture-only',
        authorization: 'Bearer local-test-only', cookie: 'session=fixture-only',
      },
    }));
    assert.equal(response.status, 404);
    assert.equal(response.headers.get('location'), null);
    assert.equal(response.headers.get('payment-required'), null);
    assert.doesNotMatch(await response.text(), /fixture-only|local-test-only/);
  }
  assert.equal(globalThis.fetch.mock.callCount(), 0);
});
