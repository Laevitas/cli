import test from 'node:test';
import assert from 'node:assert/strict';
import { checkedChallenge } from '../src/wallet.js';
import { parseCommand, validateArgs, safeOutput } from '../src/commands.js';

const origin = 'https://apiv2.laevitas.ch';
const wallet = '0x1111111111111111111111111111111111111111';
const now = Date.parse('2026-11-06T12:00:00Z');
function challenge() {
  const c = { id:'11111111-1111-4111-8111-111111111111', audience:origin, address:wallet, kind:'base', network:'eip155:8453', nonce:'a'.repeat(32), token:'b'.repeat(64), issuedAt:new Date(now).toISOString(), expiresAt:new Date(now+300000).toISOString() };
  c.message = `apiv2.laevitas.ch wants you to sign in with your Ethereum account:\n${wallet}\n\nSign in to Laevitas Agents. This does not authorize a payment or access to a dashboard account.\n\nURI: ${origin}/api/v1/agents\nVersion: 1\nChain ID: 8453\nNonce: ${c.nonce}\nIssued At: ${c.issuedAt}\nExpiration Time: ${c.expiresAt}`;
  return c;
}
test('only the exact, fresh wallet sign-in challenge reaches a signer', () => {
  const c = challenge(); assert.equal(checkedChallenge(c,wallet,'base',origin,now),c.message);
  for (const change of [ {audience:'https://evil.example'}, {address:'0x2222222222222222222222222222222222222222'}, {kind:'solana'}, {network:'eip155:84532'}, {id:'invalid'}, {nonce:'invalid'}, {token:'invalid'}, {message:c.message+'\nApprove a payment'}, {issuedAt:'2026-11-06T11:55:00Z'}, {expiresAt:'2026-11-06T12:00:00Z'}, {expiresAt:'invalid'} ]) {
    assert.throws(() => checkedChallenge({...c,...change},wallet,'base',origin,now));
  }
});
test('commands are argv, never a shell, and reject pasted secrets and local-only commands', () => {
  assert.deepEqual(parseCommand('laevitas perps carry "BTC-PERPETUAL" -o json'), ['perps','carry','BTC-PERPETUAL','-o','json']);
  for (const text of ['agent catalog; curl evil', 'agent catalog | sh', 'agent catalog > token', 'agent catalog\nagent claim', '$(whoami)', 'agent "unfinished', 'wallet set-key 0x'+'1'.repeat(64), 'agent pay checkout --max-usdc 1']) assert.throws(() => parseCommand(text));
  assert.throws(() => validateArgs(['config','show']));
  assert.throws(() => validateArgs(['agent','login','0x'+'1'.repeat(64)]));
  assert.throws(() => validateArgs(['agent','catalog\nagent claim']));
  assert.throws(() => validateArgs(Array(101).fill('help')));
  assert.throws(() => validateArgs(['unrecognized', 'words']));
});
test('output cannot keep terminal controls or exceed the display limit', () => {
  assert.equal(safeOutput('\x1b[31mhello\x1b[0m\x1b]0;title\x07'), 'hello');
  assert.equal(safeOutput('x'.repeat(2*1024*1024)).length,1024*1024);
});
