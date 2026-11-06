import http from 'node:http';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
const root = new URL('./dist/', import.meta.url);
const headers = await readFile(new URL('_headers', root), 'utf8');
const security = Object.fromEntries(headers.split('\n').filter(line => /^  [A-Za-z-]+:/.test(line)).map(line => { const i=line.indexOf(':'); return [line.slice(0,i).trim(),line.slice(i+1).trim()]; }));
delete security['Content-Type'];
const types = { '.html':'text/html', '.js':'text/javascript', '.css':'text/css', '.json':'application/json', '.svg':'image/svg+xml', '.wasm':'application/wasm' };
http.createServer(async (req, res) => {
  try {
    const pathname = new URL(req.url, 'http://localhost').pathname;
    if (req.method !== 'GET' || !/^\/[a-zA-Z0-9_.-]*$/.test(pathname)) {res.writeHead(404);res.end();return;}
    const file=pathname==='/'?'index.html':pathname.slice(1);
    const data=await readFile(new URL(file,root));
    res.writeHead(200,{...security,'Content-Type':types[path.extname(file)]||'application/octet-stream'});res.end(data);
  } catch {res.writeHead(404);res.end();}
}).listen(Number(process.env.PORT || 4173), '127.0.0.1');
