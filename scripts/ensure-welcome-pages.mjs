const account = process.env.CF_ACCOUNT_ID, token = process.env.CF_API_TOKEN;
if (!account || !token) throw new Error('Cloudflare deployment credentials are not configured.');
const base = `https://api.cloudflare.com/client/v4/accounts/${encodeURIComponent(account)}/pages/projects`;
const headers = { authorization: `Bearer ${token}`, 'content-type': 'application/json' };
const existing = await fetch(`${base}/laevitas-agents`, { headers });
if (existing.status === 404) {
  const created = await fetch(base, { method: 'POST', headers, body: JSON.stringify({ name: 'laevitas-agents', production_branch: 'main' }) });
  if (!created.ok || !(await created.json()).success) throw new Error('Unable to create the Welcome Pages project.');
} else if (!existing.ok || !(await existing.json()).success) throw new Error('Unable to inspect the Welcome Pages project.');
console.log('Welcome Pages project is ready for deployment.');
