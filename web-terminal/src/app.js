import { agentRequest, connectWallet, discoverWallets } from './wallet.js';
import { parseCommand, safeOutput, validateArgs } from './commands.js';

const $ = id => document.getElementById(id);
const dev = location.hostname === 'terminal.devitas.ch' || location.hostname.startsWith('dev.');
const origin = dev ? 'https://apiv2.devitas.ch' : 'https://apiv2.laevitas.ch';
let session = null, agentState = {}, module, worker, currentRun, expiryTimer, connectionCleanup, pendingLogin, connecting = false, generation = 0;
let cliBuild, workerURL, history = [], historyPosition = 0;
const output = $('output'), input = $('command');
$('api-origin').textContent = new URL(origin).host;
$('environment').textContent = dev ? 'DEV · test networks' : '';

function display(command, stdout = '', stderr = '') {
  const entry = document.createElement('div'); entry.className = 'entry';
  if (command) { const prompt = document.createElement('div'); prompt.className = 'command'; prompt.textContent = `› laevitas ${command}`; entry.append(prompt); }
  for (const [text, kind] of [[stdout, ''], [stderr, 'error']]) {
    if (!text) continue;
    const pre = document.createElement('pre'); pre.className = kind; pre.textContent = safeOutput(text); entry.append(pre);
  }
  output.append(entry);
  while (output.children.length > 35) output.firstChild.remove();
  output.scrollTop = output.scrollHeight;
}

function readyState() {
  const busy = !!currentRun;
  input.disabled = !module || busy; $('run').disabled = !module || busy; $('stop').disabled = !busy;
  for (const button of document.querySelectorAll('[data-command]')) button.disabled = !module || busy;
  $('runtime-status').textContent = !module ? 'Loading the CLI…' : busy ? 'Running · Ctrl+C to stop' : 'Ready · commands run in this browser';
}

function clearConnection(message = '') {
  generation++; connectionCleanup?.(); connectionCleanup = null; clearTimeout(expiryTimer);
  session = null; agentState = {}; history = []; historyPosition = 0;
  // Clear output too: Enterprise key creation can return a key once.
  output.replaceChildren(); input.value = '';
  $('connect').textContent = 'Connect wallet'; $('wallet-status').textContent = 'No wallet connected · Catalog and sample are free'; $('balance').textContent = '';
  if (currentRun) stopRun('Wallet session cleared.');
  worker?.terminate(); worker = null;
  if (pendingLogin) { pendingLogin.reject(new Error('Wallet connection changed.')); pendingLogin = null; }
  connecting = false; $('wallet-dialog').close();
  if (message) display('', message);
}

async function refreshBalance() {
  const captured = session;
  if (!captured || Date.parse(captured.expiresAt) <= Date.now()) return;
  try {
    const result = await agentRequest(origin, 'credits', { token: captured.sessionToken });
    if (captured !== session) return;
    if (Number.isSafeInteger(result.creditsRemaining)) $('balance').textContent = `${result.creditsRemaining.toLocaleString()} credits`;
  } catch (error) { if (captured === session && error.status === 401) clearConnection('Your wallet session expired. Connect again to continue.'); }
}

function ensureWallet() {
  if (session && Date.parse(session.expiresAt) > Date.now() + 1000) return Promise.resolve(session);
  if (pendingLogin) return pendingLogin.promise;
  $('wallet-error').textContent = ''; $('wallet-dialog').showModal();
  const promise = new Promise((resolve, reject) => { pendingLogin = { resolve, reject }; });
  pendingLogin.promise = promise;
  return promise;
}

function cancelWallet() {
  generation++;
  const pending = pendingLogin; pendingLogin = null; connecting = false;
  $('wallet-dialog').close(); pending?.reject(new Error('Wallet sign-in cancelled. No payment was authorized.'));
}

async function chooseWallet(wallet) {
  if (connecting || !pendingLogin) return;
  connecting = true; const attempt = generation;
  $('wallet-error').textContent = 'Approve the sign-in message in your wallet.';
  try {
    const connected = await connectWallet(wallet, origin, () => clearConnection('Wallet account or network changed. Connect again to continue.'), () => attempt === generation && !!pendingLogin);
    if (attempt !== generation || !pendingLogin) { connected.cleanup(); return; }
    session = connected.session; connectionCleanup?.(); connectionCleanup = connected.cleanup;
    $('connect').textContent = 'Disconnect';
    $('wallet-status').textContent = `${wallet.kind === 'base' ? 'Base' : 'Solana'} · ${session.wallet.slice(0, 6)}…${session.wallet.slice(-4)} · Wallet verified`;
    clearTimeout(expiryTimer); expiryTimer = setTimeout(() => clearConnection('Your wallet session expired. Connect again to continue.'), Math.max(0, Date.parse(session.expiresAt) - Date.now()));
    const pending = pendingLogin; pendingLogin = null; $('wallet-dialog').close(); pending.resolve(session);
    void refreshBalance();
  } catch (error) {
    if (attempt === generation) $('wallet-error').textContent = error?.code === 4001 ? 'Sign-in declined. No payment was requested.' : String(error?.message || 'Wallet connection failed.').slice(0, 400);
  } finally { connecting = false; }
}

discoverWallets(wallets => {
  $('wallet-list').replaceChildren();
  for (const wallet of wallets) {
    const button = document.createElement('button'); button.className = 'wallet-option'; button.type = 'button';
    const name = document.createElement('strong'); name.textContent = wallet.name;
    const chain = document.createElement('span'); chain.textContent = wallet.kind === 'base' ? 'Base / EVM' : 'Solana';
    button.append(name, chain); button.addEventListener('click', () => void chooseWallet(wallet)); $('wallet-list').append(button);
  }
});
$('close-wallet').addEventListener('click', cancelWallet);
$('wallet-dialog').addEventListener('cancel', event => { event.preventDefault(); cancelWallet(); });
$('connect').addEventListener('click', async () => {
  if (session) {
    const token = session.sessionToken; clearConnection('Disconnected. Credentials and history cleared from this tab.');
    try { await agentRequest(origin, 'auth/logout', { method: 'POST', token }); } catch { display('', '', 'Local session cleared. Server revocation could not be confirmed; the short-lived session will expire.'); }
  } else { try { await ensureWallet(); } catch (error) { display('', '', error.message); } }
});

function stopRun(message = 'Command stopped.') {
  const pending = currentRun; currentRun = null;
  if (pending) { clearTimeout(pending.timer); worker?.terminate(); worker = null; pending.reject(new Error(message)); }
  if (pendingLogin) cancelWallet();
  readyState();
}

function startWorker() {
  // Snapshot the matching Go runtime at page load. A new release cannot swap
  // wasm_exec.js underneath an already-open tab when a worker is restarted.
  const instance = new Worker(workerURL, { type: 'module' }); worker = instance;
  instance.onmessage = async ({ data }) => {
    if (worker !== instance || !currentRun) return;
    if (data.type === 'login') {
      try { const signedIn = await ensureWallet(); if (worker === instance && currentRun) instance.postMessage({ type: 'login-result', id: data.id, session: signedIn }); }
      catch { if (worker === instance && currentRun) instance.postMessage({ type: 'login-result', id: data.id, error: true }); }
      return;
    }
    if (data.type !== 'result') return;
    const pending = currentRun; currentRun = null; clearTimeout(pending.timer);
    agentState = data.state || {};
    if (data.signedOut) clearConnection('Disconnected. Credentials and history cleared from this tab.');
    const result = { stdout: safeOutput(data.stdout), stderr: safeOutput(data.stderr), exitCode: data.exitCode };
    try { result.json = JSON.parse(result.stdout); } catch {}
    pending.resolve(result); readyState(); void refreshBalance();
  };
  instance.onerror = () => stopRun('Browser CLI stopped unexpectedly. Retry the command.');
  return instance;
}

async function run({ args } = {}) {
  args = validateArgs(args);
  if (!module) throw new Error('The CLI is still loading.');
  if (currentRun) throw new Error('A command is already running. Stop it or wait for its result.');
  if (session && Date.parse(session.expiresAt) <= Date.now()) clearConnection();
  const instance = worker || startWorker();
  const promise = new Promise((resolve, reject) => {
    currentRun = { resolve, reject, timer: setTimeout(() => stopRun('Command timed out. Narrow the query or reconnect your wallet.'), 120000) };
  });
  readyState();
  instance.postMessage({ type: 'run', args, origin, session, state: agentState, module });
  return promise;
}

async function submit(line) {
  try {
    if (line.trim() === 'clear') { output.replaceChildren(); input.value = ''; return; }
    const args = parseCommand(line);
    history.push(line); history = history.slice(-50); historyPosition = history.length;
    input.value = '';
    const result = await run({ args }); display(args.join(' '), result.stdout, result.stderr);
  } catch (error) { display('', '', error.message); }
  finally { if (!input.disabled) input.focus(); }
}
$('command-form').addEventListener('submit', event => { event.preventDefault(); void submit(input.value); });
for (const button of document.querySelectorAll('[data-command]')) button.addEventListener('click', () => void submit(button.dataset.command));
$('clear').addEventListener('click', () => { output.replaceChildren(); });
$('stop').addEventListener('click', () => stopRun());
input.addEventListener('keydown', event => {
  if (event.key === 'ArrowUp') { event.preventDefault(); historyPosition = Math.max(0, historyPosition - 1); input.value = history[historyPosition] || ''; }
  if (event.key === 'ArrowDown') { event.preventDefault(); historyPosition = Math.min(history.length, historyPosition + 1); input.value = history[historyPosition] || ''; }
});
document.addEventListener('keydown', event => { if (event.ctrlKey && event.key.toLowerCase() === 'c' && currentRun) { event.preventDefault(); stopRun(); } });
window.addEventListener('pagehide', () => { worker?.terminate(); clearConnection(); });
window.addEventListener('pageshow', event => { if (event.persisted) clearConnection('Connect your wallet again after restoring this page.'); });

// Browser agents receive command output only. Tokens, wallet providers and
// session storage stay in module scope and are not part of this API.
Object.defineProperty(window, 'laevitasTerminal', { value: Object.freeze({ run, capabilities: Object.freeze({ runtime: 'wasm', payments: false, storage: 'tab-memory', apiOrigin: origin }) }), writable: false });

async function load() {
  try {
    const metadata = await fetch('/build.json', { cache: 'no-store' }).then(r => { if (!r.ok) throw new Error(); return r.json(); });
    const [bytes, runtime] = await Promise.all(['laevitas.wasm', 'worker.js'].map(async file => {
      const response = await fetch(`/${file}`, { cache: 'no-cache', credentials: 'omit', redirect: 'error' });
      if (!response.ok) throw new Error('CLI asset unavailable.');
      const buffer = await response.arrayBuffer();
      const hash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', buffer))].map(b => b.toString(16).padStart(2, '0')).join('');
      if (hash !== metadata.sha256?.[file]) throw new Error('CLI release changed while loading. Reload to retry.');
      return buffer;
    }));
    cliBuild = metadata; module = await WebAssembly.compile(bytes);
    workerURL = URL.createObjectURL(new Blob([runtime], { type: 'text/javascript' }));
    $('version').textContent = `v${metadata.version}`;
    display('', 'Welcome to Laevitas Terminal.\n\nTry: agent sample\n     agent catalog\n     agent login\n     agent balance\n\nConnect your wallet to use existing credits. Starter claims follow the live eligibility rules.\nUse --help to explore commands. No payment is signed automatically.');
    readyState(); input.focus();
  } catch { $('runtime-status').textContent = 'Unable to load the CLI. Reload to retry.'; $('runtime-status').classList.add('error-status'); display('', '', 'The browser CLI could not load. Check your connection and reload.'); }
}
readyState(); void load();
setInterval(async () => {
  if (!cliBuild || document.hidden) return;
  try { const latest = await fetch('/build.json', { cache: 'no-store' }).then(r => r.json()); if (latest.version !== cliBuild.version || latest.commit !== cliBuild.commit) $('version').textContent = `v${cliBuild.version} · update available, reload`; } catch {}
}, 5 * 60000);
