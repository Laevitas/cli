import test from 'node:test';
import assert from 'node:assert/strict';
import { configure } from '../scripts/terminal-pages.mjs';

test('domain migration snapshots existing records and touches only the two terminal names', async () => {
  const writes = []; let saved;
  await configure({ account:'account', token:'fixture', mode:'domains', snapshot:async data=>{saved=data;}, fetcher:async(url, options)=>{
    const path=new URL(url).pathname;
    if(options.method!=='GET') {assert.ok(saved);writes.push({path,...JSON.parse(options.body)});}
    let result=[];
    if(path==='/client/v4/zones') result=[{id:new URL(url).searchParams.get('name'),name:new URL(url).searchParams.get('name')}];
    if(path.endsWith('/devitas.ch/dns_records')&&options.method==='GET') result=[{id:'old',name:'terminal.devitas.ch',type:'A',content:'192.0.2.1',proxied:true}];
    return Response.json({success:true,result});
  }});
  assert.equal(saved.length,2);
  assert.equal(writes.length,2);
  assert.ok(writes.every(w=>['terminal.devitas.ch','terminal.laevitas.ch'].includes(w.name)));
  assert.equal(writes[0].path,'/client/v4/zones/devitas.ch/dns_records/old');
  assert.equal(writes[0].content,'laevitas-terminal.pages.dev');
});

test('missing zone access stops before any routing mutation and never logs credentials',async()=>{
  let writes=0;
  await assert.rejects(configure({account:'account',token:'do-not-log-me',mode:'domains',fetcher:async(_url,options)=>{
    if(options.method!=='GET')writes++;
    return Response.json({success:false,errors:[{code:10000,message:'do-not-log-me'}]},{status:403});
  }}),error=>!error.message.includes('do-not-log-me')&&error.message.includes('DNS Edit'));
  assert.equal(writes,0);
});

test('registering Pages bindings does not need DNS permissions or change DNS records',async()=>{
  const writes=[];
  await configure({account:'account',token:'fixture',mode:'bindings',fetcher:async(url,options)=>{
    assert.equal(new URL(url).pathname,'/client/v4/accounts/account/pages/projects/laevitas-terminal/domains');
    if(options.method==='GET')return Response.json({success:true,result:[{name:'terminal.devitas.ch',status:'active'}]});
    writes.push(JSON.parse(options.body));
    return Response.json({success:true,result:{status:'pending'}});
  }});
  assert.deepEqual(writes,[{name:'terminal.laevitas.ch'}]);
});

test('DNS inspection checks both zones independently of the Pages account',async()=>{
  const names=[];
  await configure({account:'pages-account',token:'fixture',mode:'status',fetcher:async(url,options)=>{
    assert.equal(options.method,'GET');
    const parsed=new URL(url);
    assert.equal(parsed.pathname,'/client/v4/zones');
    assert.equal(parsed.searchParams.has('account.id'),false);
    names.push(parsed.searchParams.get('name'));
    return Response.json({success:true,result:[]});
  }});
  assert.deepEqual(names,['devitas.ch','laevitas.ch']);
});
