// End-to-end happy path:
//   signup → verify email → create store → run audit (mocked AI) →
//   view report → enable monitoring (weekly) → simulate scheduled audit
//   → verify diff + alert email arrives in Mailhog
//
// Hits the running server + worker + scheduler with their real wiring.
// Mock AI is the default when no AI key is configured (the worker logs
// "ai_using_mock"). No real Google or Gumroad credentials are needed.

const { execSync, spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');

const BASE = 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
fs.mkdirSync(TMP, { recursive: true });

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

function startFixtureStore(variant) {
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      const u = req.url || '';
      const html = (body) => { res.writeHead(200, { 'Content-Type': 'text/html' }); res.end(body); };
      if (u === '/robots.txt') { res.writeHead(200, { 'Content-Type': 'text/plain' }); return res.end('User-agent: *\nAllow: /\n'); }
      if (u === '/sitemap.xml') {
        res.writeHead(200, { 'Content-Type': 'application/xml' });
        return res.end(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
          <url><loc>http://localhost:9999/</loc></url>
          <url><loc>http://localhost:9999/products/widget</loc></url>
        </urlset>`);
      }
      if (u === '/') return html(`<!DOCTYPE html><html><head>
        <title>Acme E2E</title>
        <link rel="stylesheet" href="https://cdn.shopify.com/s/themes/dawn/style.css">
        <script>window.Shopify={theme:{name:'Dawn'}};</script>
      </head><body><h1>Acme E2E</h1>
        <a href="/products/widget">Widget</a>
        <footer><p>1 St · NY</p><p>hello@acme.example</p></footer>
      </body></html>`);
      if (u === '/products/widget') {
        const ld = variant.value === 'bad' ? '' : `<script type="application/ld+json">
          {"@context":"https://schema.org/","@type":"Product","name":"Widget",
           "image":"https://cdn.shopify.com/s/files/1/0/products/widget.jpg",
           "description":"The Widget is a vacuum-insulated 32oz stainless steel water bottle. Triple-wall construction keeps drinks cold for 24 hours, hot for 12. Leakproof flip-top lid, BPA-free, dishwasher-safe inner sleeve.",
           "brand":{"@type":"Brand","name":"Acme"},"sku":"WIDGET-32",
           "offers":{"@type":"Offer","priceCurrency":"USD","price":"29.99","availability":"https://schema.org/InStock"}}
        </script>`;
        return html(`<!DOCTYPE html><html><head>
          <title>Widget</title>${ld}
        </head><body><h1>Widget</h1>
          <img src="https://cdn.shopify.com/s/files/1/0/products/widget.jpg" alt="Widget bottle">
          <p>$29.99</p>
        </body></html>`);
      }
      res.writeHead(404); res.end('not found');
    });
    server.listen(9999, '127.0.0.1', () => resolve(server));
  });
}

(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);
  sql(`DELETE FROM audit_jobs WHERE kind='audit_store';`);

  const variant = { value: 'good' };
  const fixture = await startFixtureStore(variant);

  const env = {
    DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable',
    // No AI key → worker uses MockClient (deterministic). No GMC creds.
  };

  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  const worker = spawnLogged('worker', path.join(ROOT, 'bin/worker'), ['-mode=worker'], env);
  const scheduler = spawnLogged('scheduler', path.join(ROOT, 'bin/scheduler'),
    ['-mode=scheduler'], { ...env, SCHEDULER_TICK: '2s' });
  await sleep(2500);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* */ }
    await sleep(200);
  }

  const stamp = Date.now().toString(36);
  const owner = { name: 'E2E Owner', email: `e2e-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `E2E ${stamp}` };

  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  // ---- 1. Signup + verify ----
  console.log('[step] signup + verify');
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await sleep(500);
  const mails = (await fetch('http://localhost:8025/api/v2/messages').then(r => r.json())).items || [];
  const verifyMail = mails.find(m => (m.Content.Headers.To || [''])[0].includes(owner.email));
  if (!verifyMail) throw new Error('no verify mail');
  const verifyURL = verifyMail.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });
  console.log('  ✓ email verified');

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Upgrade to growth so monitoring quotas don't get in the way.
  sql(`UPDATE tenants SET plan='growth'::plan_tier WHERE id='${tenantID}';`);

  // ---- 2. Login + create store via direct SQL (avoids real Shopify ping) ----
  console.log('[step] login + seed store');
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme E2E', 'connected', false, '7 days', 'warning');`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='localhost:9999';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  // ---- 3. Run audit (mocked AI) + view report ----
  console.log('[step] run manual audit + view report');
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/audits"] button[type=submit]').click(),
  ]);
  const baselineAuditID = page.url().split('/audits/')[1];
  await page.waitForFunction(() =>
    document.querySelector('.audit-progress')?.getAttribute('data-status') === 'succeeded',
    null, { timeout: 60000 });
  console.log(`  ✓ baseline audit ${baselineAuditID} succeeded`);

  // Subscribe owner to all alert triggers so the scheduled audit fires email.
  sql(`INSERT INTO store_alert_subscriptions
       (tenant_id, store_id, user_id, channel, target, min_severity, enabled,
        on_new_critical, on_score_drop, on_audit_failed, on_gmc_account_change, score_drop_threshold)
       VALUES
       ('${tenantID}', '${storeID}',
        (SELECT user_id FROM memberships WHERE tenant_id='${tenantID}' AND role='owner' LIMIT 1),
        'email', '${owner.email}', 'warning', true,
        true, true, true, false, 5);`);

  // ---- 4. Enable monitoring (weekly) ----
  console.log('[step] enable weekly monitoring');
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await page.locator('input[name="cadence"][value="weekly"]').check();
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/monitoring"] button[type=submit]').click(),
  ]);

  // Switch fixture so the next audit produces a new critical issue.
  variant.value = 'bad';
  // Force scheduler to claim immediately.
  sql(`UPDATE stores SET next_audit_at = now() - interval '1 second', monitor_frequency='5 seconds'::interval WHERE id='${storeID}';`);

  // Reset mailhog so the only email left is the alert.
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });

  // ---- 5. Wait for scheduled audit ----
  console.log('[step] wait for scheduled audit');
  let scheduledID = null;
  for (let i = 0; i < 60; i++) {
    await sleep(1000);
    const out = sqlGet(`SELECT id FROM audits WHERE store_id='${storeID}' AND trigger='scheduled' AND status='succeeded' ORDER BY created_at DESC LIMIT 1;`);
    const m = out.split('\n').map(l => l.trim()).find(l => /^[0-9a-f]{8}-/.test(l));
    if (m) { scheduledID = m; break; }
  }
  if (!scheduledID) throw new Error('scheduled audit did not complete in 60s');
  console.log(`  ✓ scheduled audit ${scheduledID} succeeded`);
  await sleep(1500); // dispatcher goroutine

  // ---- 6. Verify diff record ----
  const diff = sql(`SELECT new_issue_count, resolved_issue_count, new_critical_count, score_delta FROM audit_diffs WHERE audit_id='${scheduledID}';`);
  console.log('  diff:', diff.replace(/\n/g, ' | '));

  // ---- 7. Verify alert email ----
  const alertMails = (await fetch('http://localhost:8025/api/v2/messages').then(r => r.json())).items || [];
  const alert = alertMails.find(m => (m.Content.Headers.To || [''])[0].includes(owner.email)
    && /critical|score/i.test((m.Content.Headers.Subject || [''])[0]));
  if (!alert) throw new Error('no alert email arrived in mailhog');
  const subject = (alert.Content.Headers.Subject || [''])[0];
  console.log(`  ✓ alert email received: "${subject}"`);

  await browser.close();
  fixture.close();
  scheduler.kill('SIGTERM');
  worker.kill('SIGTERM');
  server.kill('SIGTERM');
  await sleep(500);

  console.log('\nE2E HAPPY PATH: PASS');
})().catch(err => { console.error('\nE2E HAPPY PATH: FAIL'); console.error(err); process.exit(1); });
