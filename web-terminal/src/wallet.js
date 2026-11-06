import { getWallets } from '@wallet-standard/app';

const sessionPattern = /^lva_session_[A-Za-z0-9_-]{43}$/;
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i;
const hex = bytes => Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');

export function discoverWallets(update) {
  const choices = new Map();
  const emit = () => update([...choices.values()]);
  const announce = event => {
    const { info, provider } = event.detail || {};
    if (typeof provider?.request !== 'function' || !uuid.test(info?.uuid) || typeof info.name !== 'string') return;
    choices.delete('injected');
    choices.set(info.uuid, { id: info.uuid, name: info.name.slice(0, 80), kind: 'base', provider }); emit();
  };
  window.addEventListener('eip6963:announceProvider', announce);
  if (typeof window.ethereum?.request === 'function') choices.set('injected', { id: 'injected', name: 'Browser wallet', kind: 'base', provider: window.ethereum });
  window.dispatchEvent(new Event('eip6963:requestProvider'));
  const registry = getWallets();
  const sync = () => {
    for (const [id, w] of choices) if (w.kind === 'solana') choices.delete(id);
    for (const wallet of registry.get()) {
      if (wallet.features['standard:connect'] && wallet.features['solana:signMessage']) {
        choices.set(wallet, { id: wallet, name: wallet.name.slice(0, 80), kind: 'solana', standard: wallet });
      }
    }
    emit();
  };
  const offRegister = registry.on('register', sync), offUnregister = registry.on('unregister', sync);
  sync();
  return () => { window.removeEventListener('eip6963:announceProvider', announce); offRegister(); offUnregister(); };
}

export function checkedChallenge(challenge, address, kind, origin, now = Date.now()) {
  const issued = Date.parse(challenge?.issuedAt), expires = Date.parse(challenge?.expiresAt);
  const matches = kind === 'base' ? challenge?.address?.toLowerCase() === address.toLowerCase() : challenge?.address === address;
  const dev = origin === 'https://apiv2.devitas.ch';
  const network = kind === 'base' ? (dev ? 'eip155:84532' : 'eip155:8453') : (dev ? 'solana:devnet' : 'solana:mainnet');
  if (challenge?.audience !== origin || challenge.kind !== kind || !matches || challenge.network !== network ||
      !uuid.test(challenge.id) || !/^[a-f0-9]{32}$/.test(challenge.nonce) || !/^[a-f0-9]{64}$/.test(challenge.token) ||
      !Number.isFinite(issued) || !Number.isFinite(expires) || expires <= now || expires - issued !== 300000 || Math.abs(issued - now) > 60000) {
    throw new Error('Sign-in challenge does not match this wallet, API, network or time. Nothing was signed.');
  }
  const chain = kind === 'base' ? network.split(':')[1] : network;
  const expected = `${new URL(origin).host} wants you to sign in with your ${kind === 'base' ? 'Ethereum' : 'Solana'} account:\n${challenge.address}\n\nSign in to Laevitas Agents. This does not authorize a payment or access to a dashboard account.\n\nURI: ${origin}/api/v1/agents\nVersion: 1\nChain ID: ${chain}\nNonce: ${challenge.nonce}\nIssued At: ${challenge.issuedAt}\nExpiration Time: ${challenge.expiresAt}`;
  if (challenge.message !== expected) throw new Error('Unexpected sign-in message. Nothing was signed.');
  return expected;
}

export async function agentRequest(origin, path, { method = 'GET', body, token, signal } = {}) {
  const response = await fetch(`${origin}/api/v1/agents/${path}`, {
    method, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer',
    headers: { Accept: 'application/json', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }), ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: signal || AbortSignal.timeout(15000),
  });
  const data = await response.json();
  if (!response.ok) {
    const error = new Error(typeof data.message === 'string' ? data.message.slice(0, 400) : `Request failed (HTTP ${response.status}).`);
    error.status = response.status; throw error;
  }
  return data;
}

export async function connectWallet(wallet, origin, onChange, stillCurrent) {
  let address, account, cleanup = () => {};
  if (wallet.kind === 'base') {
    const chainId = origin === 'https://apiv2.devitas.ch' ? '0x14a34' : '0x2105';
    const accounts = await wallet.provider.request({ method: 'eth_requestAccounts' });
    if (!Array.isArray(accounts) || !/^0x[0-9a-f]{40}$/i.test(accounts[0] || '')) throw new Error('Choose an Ethereum account in your wallet.');
    if (!stillCurrent()) throw new Error('Wallet connection cancelled.');
    if ((await wallet.provider.request({ method: 'eth_chainId' })).toLowerCase() !== chainId) {
      await wallet.provider.request({ method: 'wallet_switchEthereumChain', params: [{ chainId }] });
    }
    if ((await wallet.provider.request({ method: 'eth_chainId' })).toLowerCase() !== chainId) throw new Error('Select the Base network in your wallet before signing in.');
    const selected = await wallet.provider.request({ method: 'eth_accounts' });
    if (selected?.[0]?.toLowerCase() !== accounts[0].toLowerCase()) throw new Error('Wallet account changed. Connect again to continue.');
    address = accounts[0];
    const changed = () => onChange();
    wallet.provider.on?.('accountsChanged', changed); wallet.provider.on?.('chainChanged', changed); wallet.provider.on?.('disconnect', changed);
    cleanup = () => { for (const event of ['accountsChanged', 'chainChanged', 'disconnect']) wallet.provider.removeListener?.(event, changed); };
  } else {
    const result = await wallet.standard.features['standard:connect'].connect();
    const chain = origin === 'https://apiv2.devitas.ch' ? 'solana:devnet' : 'solana:mainnet';
    account = result.accounts?.find(a => a.chains?.includes(chain) && a.features?.includes('solana:signMessage'));
    if (!account || !/^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(account.address)) throw new Error('Choose a Solana account on the selected network.');
    address = account.address;
    cleanup = wallet.standard.features['standard:events']?.on('change', onChange) || cleanup;
  }
  try {
    if (!stillCurrent()) throw new Error('Wallet connection changed. Reconnect to continue.');
    const challenge = await agentRequest(origin, 'auth/challenge', { method: 'POST', body: { kind: wallet.kind, address } });
    const message = new TextEncoder().encode(checkedChallenge(challenge, address, wallet.kind, origin));
    if (!stillCurrent()) throw new Error('Wallet connection changed. Nothing was signed.');
    let signature;
    if (wallet.kind === 'base') {
      signature = await wallet.provider.request({ method: 'personal_sign', params: [`0x${hex(message)}`, address] });
      if (!/^0x[0-9a-f]{130}$/i.test(signature || '')) throw new Error('Wallet returned an invalid sign-in signature.');
    } else {
      const [signed] = await wallet.standard.features['solana:signMessage'].signMessage({ account, message });
      if (signed?.signature?.length !== 64 || signed.signedMessage?.length !== message.length || signed.signedMessage.some((b, i) => b !== message[i])) throw new Error('Wallet changed the sign-in message.');
      signature = hex(signed.signature);
    }
    if (!stillCurrent()) throw new Error('Wallet connection changed. Please reconnect.');
    const result = await agentRequest(origin, 'auth/verify', { method: 'POST', body: { challengeId: challenge.id, challengeToken: challenge.token, signature } });
    const expires = Date.parse(result.expiresAt);
    const verified = result.account?.wallet;
    const sameWallet = wallet.kind === 'base' ? verified?.address?.toLowerCase() === address.toLowerCase() : verified?.address === address;
    if (!stillCurrent() || !sessionPattern.test(result.sessionToken) || !sameWallet || verified.kind !== wallet.kind || verified.network !== challenge.network ||
        !Number.isFinite(expires) || expires <= Date.now() || expires > Date.now() + 16 * 60000 || typeof result.creditToken !== 'string' || !result.creditToken.length || result.creditToken.length > 8192) {
      if (sessionPattern.test(result.sessionToken)) void agentRequest(origin, 'auth/logout', { method: 'POST', token: result.sessionToken }).catch(() => {});
      throw new Error('Wallet verification did not return a valid session.');
    }
    return { session: { origin, wallet: address, network: challenge.network, sessionToken: result.sessionToken,
      creditToken: result.creditToken, expiresAt: result.expiresAt, account: result.account }, cleanup };
  } catch (error) { cleanup(); throw error; }
}
