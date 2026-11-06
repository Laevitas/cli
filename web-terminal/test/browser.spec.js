import { test, expect } from '@playwright/test';

const origin='https://apiv2.laevitas.ch';
const address='0x1111111111111111111111111111111111111111';
const token='lva_session_'+'a'.repeat(43), credit='test-credit-token';
async function setup(page,{badChallenge=false,walletKind='base',sessionLifetime=900000}={}) {
  const calls=[];
  const walletAddress=walletKind==='solana'?'11111111111111111111111111111111':address;
  const network=walletKind==='solana'?'solana:mainnet':'eip155:8453';
  await page.addInitScript(kind => {
    const events=new Map();window.walletCalls=[];
    if(kind==='solana') {
      const account={address:'11111111111111111111111111111111',publicKey:new Uint8Array(32),chains:['solana:mainnet'],features:['solana:signMessage']};
      const wallet={version:'1.0.0',name:'Test Solana wallet',chains:['solana:mainnet'],accounts:[account],features:{
        'standard:connect':{version:'1.0.0',connect:async()=>{window.walletCalls.push({method:'connect'});return {accounts:[account]};}},
        'solana:signMessage':{version:'1.0.0',signMessage:async({message})=>{window.walletCalls.push({method:'signMessage'});return [{signedMessage:message,signature:new Uint8Array(64)}];}},
        'standard:events':{version:'1.0.0',on:()=>()=>{}},
      }};
      window.addEventListener('wallet-standard:app-ready',event=>event.detail.register(wallet));
      return;
    }
    const provider={
      request:async({method,params})=>{window.walletCalls.push({method,params});if(['eth_requestAccounts','eth_accounts'].includes(method))return ['0x1111111111111111111111111111111111111111'];if(method==='eth_chainId')return '0x2105';if(method==='personal_sign')return '0x'+'11'.repeat(65);throw new Error('Unexpected wallet method');},
      on:(event,fn)=>{events.set(event,[...(events.get(event)||[]),fn]);},
      removeListener:(event,fn)=>events.set(event,(events.get(event)||[]).filter(x=>x!==fn)),
    };
    window.emitWallet=(event)=>{for(const f of events.get(event)||[])f([]);};
    window.addEventListener('eip6963:requestProvider',()=>window.dispatchEvent(new CustomEvent('eip6963:announceProvider',{detail:{info:{uuid:'11111111-1111-4111-8111-111111111111',name:'Test wallet'},provider}})));
  },walletKind);
  await page.route(origin+'/**',async route=>{
    const req=route.request(), pathname=new URL(req.url()).pathname;
    calls.push({path:pathname,method:req.method(),headers:req.headers()});
    let body={}, status=200;
    if(pathname.endsWith('/auth/challenge')) {
      const now=Date.now();
      body={id:'11111111-1111-4111-8111-111111111111',audience:origin,address:walletAddress,kind:walletKind,network,nonce:'a'.repeat(32),token:'b'.repeat(64),issuedAt:new Date(now).toISOString(),expiresAt:new Date(now+300000).toISOString()};
      body.message=`apiv2.laevitas.ch wants you to sign in with your ${walletKind==='base'?'Ethereum':'Solana'} account:\n${walletAddress}\n\nSign in to Laevitas Agents. This does not authorize a payment or access to a dashboard account.\n\nURI: ${origin}/api/v1/agents\nVersion: 1\nChain ID: ${walletKind==='base'?'8453':network}\nNonce: ${body.nonce}\nIssued At: ${body.issuedAt}\nExpiration Time: ${body.expiresAt}`;
      if(badChallenge)body.message+='\nSign a payment instead';
    } else if(pathname.endsWith('/auth/verify')) {
      body={sessionToken:token,creditToken:credit,expiresAt:new Date(Date.now()+sessionLifetime).toISOString(),account:{id:'fixture',wallet:{kind:walletKind,address:walletAddress,network}}};
    } else if(pathname.endsWith('/sandbox/market-snapshot')) body={synthetic:true,price:100};
    else if(pathname.endsWith('/credits/claim')) {status=403;body={code:'starter_ineligible',message:'Starter credits require an established wallet or a qualifying purchase.'};}
    else if(pathname.endsWith('/credits'))body={creditsRemaining:1000};
    else if(pathname.endsWith('/agents/catalog'))body={stage:'live',plans:[]};
    else if(pathname.endsWith('/perpetuals/carry'))body={data:[{instrument_name:'BTC-PERPETUAL',funding_rate_close:0.0001}]};
    else if(pathname.endsWith('/options/catalog')){status=402;body={error:'payment_required',x402Version:2};}
    else if(pathname.endsWith('/me'))body={wallet:{kind:'base',address,network:'eip155:8453'}};
    await route.fulfill({status,contentType:'application/json',headers:{'access-control-allow-origin':'*','x-credits-remaining':'999'},body:JSON.stringify(body)});
  });
  await page.goto('/?command=agent%20claim');
  await expect(page.locator('#command')).toBeEnabled({timeout:45000});
  return calls;
}

async function login(page,walletName='Test wallet Base / EVM') {
  await page.evaluate(()=>{window.loginResult=window.laevitasTerminal.run({args:['agent','login']});});
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button',{name:walletName}).click();
  const result=await page.evaluate(()=>window.loginResult);
  expect(result.exitCode).toBe(0);expect(result.json.data.authenticated).toBe(true);
  expect(result.stdout).not.toContain(token);expect(result.stdout).not.toContain(credit);
}

test('real WASM CLI runs without a wallet; query strings never execute commands',async({page})=>{
  const calls=await setup(page);
  expect(calls.some(x=>x.path.endsWith('/credits/claim'))).toBe(false);
  const result=await page.evaluate(()=>window.laevitasTerminal.run({args:['agent','sample']}));
  expect(result.exitCode).toBe(0);expect(result.json.data.synthetic).toBe(true);
  const version=await page.evaluate(()=>window.laevitasTerminal.run({args:['version']}));
  expect(version.stdout).toContain(await page.locator('#version').textContent());
  expect(await page.evaluate(()=>window.walletCalls)).toEqual([]);
  await page.getByRole('button',{name:'Try free sample',exact:true}).click();
  await expect(page.locator('#output')).toContainText('synthetic');
  expect(await page.locator('.brand img').evaluate(img=>img.naturalWidth)).toBeGreaterThan(0);
  await page.screenshot({path:'artifacts/desktop.png',fullPage:true});
});

test('wallet sign-in uses the existing session and credits without payment signing or durable storage',async({page,context})=>{
  await context.addCookies([{name:'unrelated-dashboard-cookie',value:'private-fixture',domain:'apiv2.laevitas.ch',path:'/'}]);
  const calls=await setup(page);await login(page);
  const data=await page.evaluate(()=>window.laevitasTerminal.run({args:['perps','carry','BTC-PERPETUAL','-p','1h','-n','1','-o','json']}));
  expect(data.exitCode).toBe(0);expect(data.json.success).toBe(true);expect(data.json.data).toHaveLength(1);
  const request=calls.find(x=>x.path.endsWith('/perpetuals/carry'));
  expect(request.headers['x-credit-token']).toBe(credit);expect(request.headers.cookie).toBeUndefined();expect(request.headers.authorization).toBeUndefined();
  const rejected=await page.evaluate(()=>window.laevitasTerminal.run({args:['agent','claim']}));
  expect(rejected.exitCode).toBe(1);expect(rejected.stdout).toContain('established wallet');
  const unpaid=await page.evaluate(()=>window.laevitasTerminal.run({args:['options','catalog','-o','json']}));
  expect(unpaid.exitCode).toBe(1);expect(unpaid.json.error.code).toBe('PAYMENT_REQUIRED');
  expect((await page.evaluate(()=>window.walletCalls)).map(x=>x.method)).toEqual(['eth_requestAccounts','eth_chainId','eth_chainId','eth_accounts','personal_sign']);
  expect(await page.evaluate(()=>({local:localStorage.length,session:sessionStorage.length}))).toEqual({local:0,session:0});
  await page.reload();await expect(page.locator('#command')).toBeEnabled({timeout:45000});
  await expect(page.locator('#wallet-status')).toContainText('No wallet connected');
});

test('mismatched sign-in messages never reach the wallet',async({page})=>{
  await setup(page,{badChallenge:true});
  await page.getByRole('button',{name:'Connect wallet',exact:true}).click();
  await page.getByRole('button',{name:'Test wallet Base / EVM'}).click();
  await expect(page.locator('#wallet-error')).toContainText('Unexpected sign-in message');
  expect((await page.evaluate(()=>window.walletCalls)).some(x=>x.method==='personal_sign')).toBe(false);
});

test('wallet changes clear credentials and redirects cannot carry tokens elsewhere',async({page})=>{
  await setup(page);await login(page);
  let externalRequests=0;
  await page.route('https://evil.example/**',route=>{externalRequests++;return route.abort();});
  await page.route(origin+'/api/v1/perpetuals/carry**',route=>route.fulfill({status:307,headers:{location:'https://evil.example/steal','access-control-allow-origin':'*'}}));
  const result=await page.evaluate(()=>window.laevitasTerminal.run({args:['perps','carry','BTC-PERPETUAL','-o','json']}));
  expect(result.exitCode).toBe(1);expect(externalRequests).toBe(0);
  await page.evaluate(()=>window.emitWallet('accountsChanged'));
  await expect(page.locator('#wallet-status')).toContainText('No wallet connected');
  const noSession=await page.evaluate(()=>window.laevitasTerminal.run({args:['perps','carry','BTC-PERPETUAL','-o','json']}));
  expect(noSession.exitCode).toBe(1);expect(noSession.stderr).toContain('Connect your browser wallet');
});

test('mobile commands remain usable with no horizontal overflow',async({page})=>{
  await page.setViewportSize({width:390,height:844});await setup(page);
  await page.getByRole('button',{name:'Try free sample',exact:true}).click();
  await expect(page.locator('#output')).toContainText('synthetic');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:'artifacts/mobile.png',fullPage:true});
});

test('Solana Wallet Standard signs only the checked message and uses the same credits',async({page})=>{
  const calls=await setup(page,{walletKind:'solana'});await login(page,'Test Solana wallet Solana');
  const result=await page.evaluate(()=>window.laevitasTerminal.run({args:['agent','balance']}));
  expect(result.json.data.creditsRemaining).toBe(1000);
  expect(calls.find(x=>x.path.endsWith('/credits')).headers.authorization).toBe('Bearer '+token);
  expect((await page.evaluate(()=>window.walletCalls)).map(x=>x.method)).toEqual(['connect','signMessage']);
  await expect(page.locator('#wallet-status')).toContainText('Solana');
});

test('logout help has no side effects; logout with global flags clears the tab and its Worker',async({page})=>{
  await setup(page);await login(page);
  await page.evaluate(()=>window.laevitasTerminal.run({args:['agent','logout','--help']}));
  await expect(page.locator('#wallet-status')).toContainText('Wallet verified');
  await page.evaluate(()=>window.laevitasTerminal.run({args:['-o','json','agent','logout']}));
  await expect(page.locator('#wallet-status')).toContainText('No wallet connected');
  // A new deployment cannot replace the runtime used by an open tab.
  await page.route('**/worker.js',route=>route.fulfill({body:'throw new Error("new incompatible release")',contentType:'text/javascript'}));
  const sample=await page.evaluate(()=>window.laevitasTerminal.run({args:['agent','sample']}));
  expect(sample.exitCode).toBe(0);
});

test('cancelled claims never reach the API; session expiry clears tab credentials',async({page})=>{
  const calls=await setup(page,{sessionLifetime:1500});
  await page.evaluate(()=>{window.claimResult=window.laevitasTerminal.run({args:['agent','claim']});});
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button',{name:'Close wallet selection'}).click();
  expect((await page.evaluate(()=>window.claimResult)).exitCode).toBe(1);
  expect(calls.some(x=>x.path.endsWith('/credits/claim'))).toBe(false);
  await login(page);
  await expect(page.locator('#wallet-status')).toContainText('No wallet connected',{timeout:5000});
  const data=await page.evaluate(()=>window.laevitasTerminal.run({args:['perps','carry','BTC-PERPETUAL']}));
  expect(data.exitCode).toBe(1);expect(data.stderr).toContain('Connect your browser wallet');
});
