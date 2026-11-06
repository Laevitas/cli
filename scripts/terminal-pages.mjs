// Only the two explicitly requested terminal hostnames are managed here.
import { mkdir, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

export const domains = ['terminal.devitas.ch', 'terminal.laevitas.ch'];
const project = 'laevitas-terminal';
const target = `${project}.pages.dev`;

export async function configure({ account, token, mode, fetcher = fetch, snapshot = async () => {} }) {
  if (!account || !token || !['project', 'domains'].includes(mode)) throw new Error('Cloudflare account, token and project/domains mode are required.');
  const base = `/accounts/${encodeURIComponent(account)}/pages/projects`;
  async function api(path, method = 'GET', body, missing = false) {
    const response = await fetcher(`https://api.cloudflare.com/client/v4${path}`, {
      method, headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }), redirect: 'error', signal: AbortSignal.timeout(30000),
    });
    if (missing && response.status === 404) return null;
    const json = await response.json();
    // Never log headers, credentials or arbitrary provider response bodies.
    if (!response.ok || !json.success) throw new Error(`Cloudflare ${method} ${path} failed (HTTP ${response.status}; codes ${(json.errors || []).map(e => e.code).join(',')}). Check Pages Edit and Zone Read / DNS Edit for the two terminal zones.`);
    return json.result;
  }
  if (mode === 'project') {
    if (!await api(`${base}/${project}`, 'GET', undefined, true)) await api(base, 'POST', { name: project, production_branch: 'main' });
    console.log('Terminal Pages project is ready.');
    return;
  }
  // Inspect both zones before changing routing. A missing DNS permission must
  // not silently leave half the migration applied.
  const plans = [];
  for (const name of domains) {
    const zoneName = name.slice('terminal.'.length);
    const zones = await api(`/zones?name=${encodeURIComponent(zoneName)}&account.id=${encodeURIComponent(account)}`);
    const zone = zones.find(z => z.name === zoneName);
    if (!zone) throw new Error(`No accessible Cloudflare zone for ${zoneName}. Zone Read and DNS Edit are needed for terminal routing.`);
    const path = `/zones/${zone.id}/dns_records`;
    const records = await api(`${path}?name=${encodeURIComponent(name)}`);
    if (records.length > 1 || records.some(r => r.name !== name || !['A', 'AAAA', 'CNAME'].includes(r.type))) throw new Error(`Review conflicting DNS records for ${name} before changing routing.`);
    plans.push({ name, path, records });
  }
  await snapshot(plans.map(({ name, records }) => ({ name, records })));
  const bindings = await api(`${base}/${project}/domains`);
  for (const { name, path, records } of plans) {
    if (!bindings.some(d => d.name === name)) await api(`${base}/${project}/domains`, 'POST', { name });
    // Pages can create the CNAME itself when attaching an in-account zone.
    const current = await api(`${path}?name=${encodeURIComponent(name)}`);
    if (current.length > 1 || current.some(r => r.name !== name || !['A','AAAA','CNAME'].includes(r.type))) throw new Error(`DNS changed during migration for ${name}; review before retrying.`);
    const record = current[0];
    if (record?.type !== 'CNAME' || record.content !== target || !record.proxied) {
      const data = { type: 'CNAME', name, content: target, proxied: true, ttl: 1 };
      await api(record ? `${path}/${record.id}` : path, record ? 'PUT' : 'POST', data);
    }
    console.log(`${name} → ${target}; TLS activation may take a few minutes.`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await configure({ account: process.env.CF_ACCOUNT_ID, token: process.env.CF_API_TOKEN, mode: process.argv[2],
    snapshot: async data => { await mkdir('web-terminal/artifacts', { recursive: true }); await writeFile('web-terminal/artifacts/terminal-dns-before.json', JSON.stringify(data, null, 2)); } });
}
