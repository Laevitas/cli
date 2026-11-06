import './wasm_exec.js';

let module, origin, active = false, signedOut = false, session = '', state = {}, sequence = 0;
const pending = new Map();
const rawFetch = globalThis.fetch.bind(globalThis);
globalThis.fetch = (input, options = {}) => {
  const url = new URL(typeof input === 'string' ? input : input.url);
  if (url.origin !== origin || !url.pathname.startsWith('/api/v1/') || url.username || url.password) return Promise.reject(new Error('Request outside the configured Laevitas API was refused.'));
  return rawFetch(input, { ...options, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer' });
};

globalThis.laevitasBrowser = {
  get session() { return session; },
  load: key => state[key] || '',
  save: (key, value) => { state[key] = value; },
  clear: () => { session = ''; state = {}; signedOut = true; },
  login: requestedOrigin => new Promise((resolve, reject) => {
    if (requestedOrigin !== origin) { reject(new Error('Wrong sign-in origin.')); return; }
    const id = ++sequence; pending.set(id, { resolve, reject }); postMessage({ type: 'login', id });
  }),
};

onmessage = async ({ data }) => {
  if (data.type === 'login-result') {
    const callbacks = pending.get(data.id); if (!callbacks) return;
    pending.delete(data.id);
    if (data.error) callbacks.reject(new Error('Sign-in cancelled.'));
    else { session = JSON.stringify(data.session); callbacks.resolve(session); }
    return;
  }
  if (data.type !== 'run' || active) return;
  active = true; signedOut = false; origin = data.origin; session = JSON.stringify(data.session || null); state = data.state || {};
  let stdout = '', stderr = '', exitCode = 0;
  const decoder = new TextDecoder();
  fs.writeSync = (fd, bytes) => {
    const text = decoder.decode(bytes, { stream: true });
    if (stdout.length + stderr.length + text.length > 1024 * 1024) throw new Error('Output exceeded the browser limit. Narrow the query.');
    if (fd === 2) stderr += text; else stdout += text;
    return bytes.length;
  };
  try {
    module ||= data.module;
    const go = new Go();
    go.argv = ['laevitas', ...data.args];
    go.env = { HOME: '/browser', LAEVITAS_CONFIG_DIR: '/browser', LAEVITAS_BASE_URL: origin, LAEVITAS_AUTH: 'credits', TERM: 'dumb', NO_COLOR: '1' };
    go.exit = code => { exitCode = code; };
    const instance = await WebAssembly.instantiate(module, go.importObject);
    await go.run(instance);
    postMessage({ type: 'result', stdout, stderr, exitCode, state, signedOut });
  } catch { postMessage({ type: 'result', stdout, stderr: 'Browser CLI could not finish. Retry or narrow the query.', exitCode: 1, state, signedOut }); }
  finally { active = false; }
};
