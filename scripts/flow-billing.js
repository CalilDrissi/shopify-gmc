// Turn-1 billing demo:
//   1. Boot a fresh server with Gumroad env wired
//   2. Sign up + verify a tenant owner (starts on Free)
//   3. Visit /pricing — screenshot the upgrade buttons
//   4. Visit /t/{slug}/billing — screenshot the comparison grid + "current plan" card
//   5. POST a signed Gumroad webhook (a "sale" subscription event for Growth)
//   6. Re-visit billing — confirm plan flipped to Growth + purchase row appeared
//   7. POST a "refund" webhook for the same sale_id, confirm downgrade to Free
//   8. POST the same "sale" again, confirm dedup (no double-flip)
//   9. Trigger a 402 by exceeding the Free plan's audit quota

const { execSync, spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const BASE = 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
const OUT = path.join(TMP, 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

const sql = (s) => execSync(`docker exec gmcauditor-postgres psql -U gmc -d gmcauditor -c "${s}"`, { encoding: 'utf8' });
const sqlGet = (s) => sql(s).trim();
function spawnLogged(name, bin, args, env) {
  const out = fs.createWriteStream(path.join(TMP, name + '.log'));
  const p = spawn(bin, args, { env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  p.stdout.pipe(out, { end: false });
  p.stderr.pipe(out, { end: false });
  return p;
}
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

// HMAC-SHA256 of body, hex-encoded — matches billing.SignBody.
function signBody(secret, body) {
  return crypto.createHmac('sha256', secret).update(body).digest('hex');
}

// ---------------------------------------------------------------------------
// Demo
// ---------------------------------------------------------------------------
(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });

  const webhookSecret = 'demo-webhook-secret-' + Date.now();
  const env = {
    DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable',
    GUMROAD_WEBHOOK_SECRET: webhookSecret,
    GUMROAD_PRODUCT_STARTER: 'gmc-starter',
    GUMROAD_PRODUCT_GROWTH:  'gmc-growth',
    GUMROAD_PRODUCT_AGENCY:  'gmc-agency',
    GUMROAD_PRODUCT_RESCUE:  'gmc-rescue',
    GUMROAD_PRODUCT_DFY:     'gmc-dfy',
  };

  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  await sleep(2500);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* */ }
    await sleep(200);
  }

  // Sign up + verify
  const stamp = Date.now().toString(36);
  const owner = { name: 'Bea Billing', email: `bea-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Bea ${stamp}` };
  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
  const page = await ctx.newPage();

  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await sleep(500);
  const mailItems = (await fetch('http://localhost:8025/api/v2/messages').then(r => r.json())).items;
  const mail = mailItems.find(m => (m.Content.Headers.To || [''])[0].includes(owner.email));
  const verifyURL = mail.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  console.log('tenant:', tenantID, 'slug:', slug);

  // Login
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  // /pricing screenshot
  await page.goto(`${BASE}/pricing`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, 'A1-pricing.png'), fullPage: true });

  // /billing screenshot — Free plan
  await page.goto(`${BASE}/t/${slug}/billing`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, 'A2-billing-free.png'), fullPage: true });

  // ---- Webhook: sale (Growth subscription) ----
  const saleID = 'demo-sale-' + stamp;
  const saleBody = new URLSearchParams({
    sale_id: saleID,
    product_permalink: 'gmc-growth',
    email: owner.email,
    price_cents: '4900',
    currency: 'USD',
    sale_timestamp: new Date().toISOString(),
    recurrence: 'monthly',
    subscription_id: 'demo-sub-' + stamp,
    'tenant_id': tenantID,
    'user_email': owner.email,
  }).toString();

  console.log('\n=== webhook 1 (sale, Growth) ===');
  const sig1 = signBody(webhookSecret, saleBody);
  const r1 = await fetch(BASE + '/webhooks/gumroad', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Gumroad-Signature': sig1 },
    body: saleBody,
  });
  console.log('  HTTP', r1.status);
  console.log('  tenant.plan:', sqlGet(`SELECT plan FROM tenants WHERE id='${tenantID}';`));

  // ---- Replay (dedup) ----
  console.log('\n=== webhook 2 (replay, dedup) ===');
  const r2 = await fetch(BASE + '/webhooks/gumroad', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Gumroad-Signature': sig1 },
    body: saleBody,
  });
  console.log('  HTTP', r2.status);
  console.log('  webhook event count for sale_id:',
    sqlGet(`SELECT count(*) FROM gumroad_webhook_events WHERE sale_id='${saleID}';`).split('\n').filter(l => /^\s*\d/.test(l))[0].trim());

  // ---- Billing page after upgrade ----
  await page.goto(`${BASE}/t/${slug}/billing`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, 'A3-billing-growth.png'), fullPage: true });

  // ---- Bad signature → 401 ----
  console.log('\n=== webhook 3 (bad signature) ===');
  const r3 = await fetch(BASE + '/webhooks/gumroad', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Gumroad-Signature': 'deadbeef' },
    body: saleBody,
  });
  console.log('  HTTP', r3.status);

  // ---- Refund ----
  console.log('\n=== webhook 4 (refund) ===');
  const refundBody = new URLSearchParams({
    resource_name: 'refund',
    sale_id: saleID,
    refunded: 'true',
    refunded_at: new Date().toISOString(),
    'tenant_id': tenantID,
  }).toString();
  const sig4 = signBody(webhookSecret, refundBody);
  const r4 = await fetch(BASE + '/webhooks/gumroad', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'X-Gumroad-Signature': sig4 },
    body: refundBody,
  });
  console.log('  HTTP', r4.status);
  console.log('  tenant.plan:', sqlGet(`SELECT plan FROM tenants WHERE id='${tenantID}';`));
  console.log('  purchase status:', sqlGet(`SELECT status FROM purchases WHERE gumroad_sale_id='${saleID}';`));

  // ---- Plan limit 402 — exhaust Free's 1-audit/mo quota ----
  // Inject a fake usage_counters row at the cap to skip running real audits.
  sql(`SELECT app_increment_usage('${tenantID}', 'audits', 1);`);
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'free-quota-store.example.com', 'Free Quota Test', 'connected', false, '7 days', 'warning')
       ON CONFLICT DO NOTHING;`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='free-quota-store.example.com';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  console.log('\n=== plan-limit 402 ===');
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  // POST the audit run
  const csrf = await page.locator('input[name=_csrf]').first().inputValue();
  const cookieHeader = (await ctx.cookies()).map(c => `${c.name}=${c.value}`).join('; ');
  const r5 = await fetch(`${BASE}/t/${slug}/stores/${storeID}/audits`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded', Cookie: cookieHeader },
    redirect: 'manual',
    body: new URLSearchParams({ _csrf: csrf }).toString(),
  });
  console.log('  HTTP', r5.status);
  // Render the 402 page in the browser for the screenshot
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  // Trigger via form post
  await page.locator('form[action$="/audits"] button[type=submit]').click({ noWaitAfter: true });
  await sleep(800);
  await page.screenshot({ path: path.join(OUT, 'A4-plan-limit-402.png'), fullPage: true });

  console.log('\n=== gumroad_webhook_events ===');
  console.log(sql(`SELECT event_type, sale_id, signature_ok, processed_at IS NOT NULL AS processed, error_message FROM gumroad_webhook_events ORDER BY received_at DESC LIMIT 5;`));

  await browser.close();
  server.kill('SIGTERM');
  await sleep(500);
})().catch(err => { console.error(err); process.exit(1); });
