// Full end-to-end monitoring + alerts demo:
//   1. Sign up + verify a fresh user
//   2. Inject a Shopify-fixture store
//   3. Boot server + worker + scheduler (5s tick)
//   4. UI: monitoring card → toggle cadence "weekly" → save
//   5. UI: alerts card → keep all triggers checked → save
//   6. Wait for scheduler to enqueue + worker to complete two scheduled audits
//   7. Screenshot: store-detail (monitoring + trendline), audit detail (changes section), audits-list (source + diff cols), alert email rendering

const { execSync, spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
const OUT = path.join(TMP, 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

function startFixtureStore(variant) {
  // variant: 'good' (initial fixture) or 'bad' (drops product schema to provoke new criticals on second audit)
  return new Promise((resolve) => {
    const server = http.createServer((req, res) => {
      const u = req.url || '/';
      const html = (body) => { res.writeHead(200, { 'Content-Type': 'text/html' }); res.end(body); };
      if (u === '/robots.txt') { res.writeHead(200, { 'Content-Type': 'text/plain' }); return res.end('User-agent: *\nAllow: /\n'); }
      if (u === '/sitemap.xml') {
        res.writeHead(200, { 'Content-Type': 'application/xml' });
        return res.end(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
          <url><loc>http://localhost:9999/</loc></url>
          <url><loc>http://localhost:9999/products/widget</loc></url>
          <url><loc>http://localhost:9999/policies/refund-policy</loc></url>
          <url><loc>http://localhost:9999/policies/privacy-policy</loc></url>
        </urlset>`);
      }
      if (u === '/') return html(`<!DOCTYPE html><html><head>
        <title>Acme Goods</title>
        <link rel="canonical" href="http://localhost:9999/">
        <link rel="stylesheet" href="https://cdn.shopify.com/s/themes/dawn/style.css">
        <script>window.Shopify={theme:{name:'Dawn'}};</script>
      </head><body><h1>Acme Goods</h1>
        <a href="/products/widget">Widget</a>
        <footer>
          <p>123 Market St · Brooklyn NY 11201</p>
          <p><a href="mailto:hello@acme.example">hello@acme.example</a></p>
          <a href="/policies/refund-policy">Returns</a>
          <a href="/policies/privacy-policy">Privacy</a>
        </footer>
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
          <title>Widget</title>
          <link rel="canonical" href="http://localhost:9999/products/widget">
          ${ld}
        </head><body><h1>Widget</h1>
          <img src="https://cdn.shopify.com/s/files/1/0/products/widget.jpg" alt="Widget bottle">
          <p>$29.99</p>
        </body></html>`);
      }
      if (u === '/policies/refund-policy') return html('<html><body><h1>Returns</h1><p>30-day returns.</p></body></html>');
      if (u === '/policies/privacy-policy') return html("<html><body><h1>Privacy</h1><p>We don't sell data.</p></body></html>");
      res.writeHead(404); res.end('not found');
    });
    server.listen(9999, '127.0.0.1', () => resolve(server));
  });
}

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

async function fetchMailFor(email) {
  const data = await fetch('http://localhost:8025/api/v2/messages').then(r => r.json());
  return (data.items || []).filter(m => ((m.Content.Headers.To || [''])[0] || '').includes(email));
}

(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);
  sql(`DELETE FROM audit_jobs WHERE kind='audit_store';`);

  const variant = { value: 'good' };
  const fixture = await startFixtureStore(variant);

  const stamp = Date.now().toString(36);
  const owner = { name: 'Wendy Mon', email: `wendy-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Acme ${stamp}` };

  const dbURL = 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable';
  const env = { DATABASE_URL: dbURL };

  console.log('booting server, worker, scheduler (tick=2s)...');
  const server    = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  const worker    = spawnLogged('worker', path.join(ROOT, 'bin/worker'), ['-mode=worker'], env);
  const scheduler = spawnLogged('scheduler', path.join(ROOT, 'bin/scheduler'), ['-mode=scheduler'], { ...env, SCHEDULER_TICK: '2s' });
  await sleep(2500);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* retry */ }
    await sleep(200);
  }

  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  // Sign up + verify
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await sleep(500);
  const verifyMail = (await fetchMailFor(owner.email))[0];
  const verifyURL = verifyMail.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Upgrade tenant to 'agency' so all cadences are unlocked.
  sql(`UPDATE tenants SET plan='agency'::plan_tier WHERE id='${tenantID}';`);

  // Inject the fixture store with monitor_enabled=false (we'll toggle via UI).
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme E2E', 'connected', false, '7 days', 'warning');`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='localhost:9999';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Login
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  // Open the store. Initially monitoring is off.
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '80-monitoring-off.png'), fullPage: true });

  // Run a manual audit first so we have history for the trendline + a "previous" baseline.
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/audits"] button[type=submit]').click(),
  ]);
  await page.waitForFunction(() =>
    document.querySelector('.audit-progress')?.getAttribute('data-status') === 'succeeded'
    , null, { timeout: 60000 });

  // Switch fixture to the "bad" variant so the second audit produces new critical issues.
  variant.value = 'bad';

  // Back to store. Toggle cadence to "weekly" and submit.
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await page.locator('input[name="cadence"][value="weekly"]').check();
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/monitoring"] button[type=submit]').click(),
  ]);
  // Force the next_audit_at to NOW so the scheduler picks it up immediately
  // (the UI sets it but at the cadence interval into the future).
  sql(`UPDATE stores SET next_audit_at = now() - interval '1 second' WHERE id='${storeID}';`);
  // Save subscription preferences (already defaults to all-checked except gmc; submit form so a row exists).
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await page.locator('form[action$="/subscriptions"] button[type=submit]').click();
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: path.join(OUT, '81-monitoring-weekly.png'), fullPage: true });

  // Wait for the scheduler to enqueue + worker to complete a scheduled audit.
  console.log('waiting for scheduled audit...');
  let scheduledAuditID = null;
  for (let i = 0; i < 40; i++) {
    await sleep(1000);
    const out = sqlGet(`SELECT id FROM audits WHERE store_id='${storeID}' AND trigger='scheduled' AND status='succeeded' ORDER BY created_at DESC LIMIT 1;`);
    const m = out.split('\n').map(l => l.trim()).find(l => /^[0-9a-f]{8}-/.test(l));
    if (m) { scheduledAuditID = m; break; }
  }
  if (!scheduledAuditID) throw new Error('scheduled audit did not complete in time');
  console.log('scheduled audit done:', scheduledAuditID);
  await sleep(1500); // dispatcher goroutine

  // Open the scheduled audit's detail page — should show "Changes since last audit" card.
  await page.goto(`${BASE}/t/${slug}/audits/${scheduledAuditID}`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '82-audit-with-changes.png'), fullPage: true });

  // Audits list — should show source + diff columns with the new scheduled audit.
  await page.goto(`${BASE}/t/${slug}/audits`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '83-audits-list-source-diff.png'), fullPage: true });

  // Alert email — fetch from mailhog and screenshot.
  const mails = await fetchMailFor(owner.email);
  const alertMail = mails.find(m => /critical|score|failed/i.test((m.Content.Headers.Subject || [''])[0]));
  if (alertMail) {
    const decoded = alertMail.Content.Body
      .replace(/=\r?\n/g, '')
      .replace(/=([0-9A-Fa-f]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
    const m = decoded.match(/<!DOCTYPE html>[\s\S]+<\/html>/i);
    if (m) {
      fs.writeFileSync(path.join(TMP, 'alert-email.html'), m[0]);
      await page.goto('file://' + path.join(TMP, 'alert-email.html'), { waitUntil: 'networkidle' });
      await page.screenshot({ path: path.join(OUT, '84-alert-email.png'), fullPage: true });
    }
  } else {
    console.warn('no alert email found yet');
  }

  console.log('\n=== alert_dispatches ===');
  console.log(sql(`SELECT substr(audit_id::text,1,8) AS audit, trigger, target, sent_at::time FROM alert_dispatches WHERE tenant_id='${tenantID}' ORDER BY sent_at;`));
  console.log('\n=== latest audit_diffs ===');
  console.log(sql(`SELECT substr(audit_id::text,1,8) AS a, substr(coalesce(previous_audit_id::text,'-'),1,8) AS prev, new_issue_count, resolved_issue_count, new_critical_count, prev_score, new_score, score_delta FROM audit_diffs WHERE tenant_id='${tenantID}' ORDER BY created_at;`));
  console.log('\n=== scheduler tail ===');
  console.log(execSync(`tail -n 10 ${path.join(TMP, 'scheduler.log')}`, { encoding: 'utf8' }));

  await browser.close();
  fixture.close();
  scheduler.kill('SIGTERM');
  worker.kill('SIGTERM');
  server.kill('SIGTERM');
  await sleep(500);
  console.log(JSON.stringify({ owner: owner.email, slug, tenantID, storeID, scheduledAuditID }, null, 2));
})().catch(err => { console.error(err); process.exit(1); });
