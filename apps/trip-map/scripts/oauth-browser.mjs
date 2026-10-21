import { chromium } from '@playwright/test';
import assert from 'node:assert/strict';

const [bff, a, b] = process.argv.slice(2);
const browser = await chromium.launch({ headless: true, args: ["--host-resolver-rules=MAP *.example.test 127.0.0.1", "--no-proxy-server"] });
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, proxy: { server: "http://127.0.0.1:9", bypass: ".example.test,localhost,127.0.0.1" } });
  const page = await context.newPage();
  let user;
  const rpc = async (method, token, expectedUser = user) => page.evaluate(async ({bff,method,token,expectedUser}) => {
    const headers = { 'Content-Type':'application/json', 'Connect-Protocol-Version':'1' };
    if(token) headers['X-CSRF-Token']=token;
    if(expectedUser && method !== 'Me') headers['X-Kongken-User']=expectedUser;
    const res=await fetch(`${bff}/api/bff.v1.AuthService/${method}`,{method:'POST',credentials:'include',headers,body:'{}'});
    return {status:res.status, body:await res.json()};
  }, {bff,method,token,expectedUser});
  for (const [app, origin, path] of [['a',a,'/'],['b',b,'/map']]) {
    await page.goto(`${bff}/auth/login?app=${app}`);
    await page.waitForURL(origin+path);
    assert.equal(new URL(page.url()).search,'');
    const me=await rpc('Me');
    assert.equal(me.status,200);
    assert.ok(me.body.user.id);
    if(user) assert.equal(me.body.user.id,user);
    user=me.body.user.id;
    assert.equal((await rpc('Logout')).status,403);
    assert.equal((await rpc('Logout','wrong')).status,403);
    const csrf=await rpc('IssueCSRFToken');
    assert.equal(csrf.status,200);
    assert.equal((await rpc("RevokeSession",csrf.body.token,"previous-account")).status,401);
    assert.equal((await rpc('RevokeSession',csrf.body.token)).status,404); // CSRF passed, missing session id
    const cookies=await context.cookies(bff);
    const sid=cookies.find(c=>c.name==='kongken_sid');
    assert.ok(sid?.httpOnly && sid.secure && sid.sameSite==='Lax');
    assert.equal((await context.cookies(origin)).find(c=>c.name==='kongken_sid'),undefined,'BFF cookie leaked to app host');
    assert.equal(cookies.filter(c=>c.name.includes('kongken_oauth_')).length,0);
    assert.equal(await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage})), '{}');
    assert.equal((await rpc('Logout',csrf.body.token)).status,200);
    assert.equal((await rpc('Me')).status,401);
  }
  // An unlisted HTTPS origin gets neither readable success nor readable errors.
  await page.goto(a.replace('a.example.test','evil.example.test'));
  await assert.rejects(()=>rpc('Me'),/fetch/i);
  console.log('Two HTTPS origins: PKCE, binding cookie, CORS preflight, Me, CSRF and logout passed.');
  await context.close();
} finally { await browser.close(); }
