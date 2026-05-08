// Alerts demo:
//   1. Sign up + verify a fresh user
//   2. Inject a Shopify-fixture store
//   3. Subscribe the owner to all four alert triggers
//   4. Trigger one audit (manually) — owner gets a "new_critical" email
//   5. Trigger a second audit immediately — same fingerprint, no new criticals,
//      but if any score-drop alert ran, the 24h rate limit suppresses it
//   6. Print: dispatcher logs, alert_dispatches rows, the fetched email HTML

const { execSync, spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
fs.mkdirSync(TMP, { recursive: true });

function startFixtureStore() {
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
      if (u === '/products/widget') return html(`<!DOCTYPE html><html><head>
        <title>Widget</title>
        <link rel="canonical" href="http://localhost:9999/products/widget">
        <script type="application/ld+json">
        {"@context":"https://schema.org/","@type":"Product","name":"Widget",
          "image":"https://cdn.shopify.com/s/files/1/0/products/widget.jpg",
          "description":"The Widget is a vacuum-insulated 32oz stainless steel water bottle. Triple-wall construction keeps drinks cold for 24 hours, hot for 12. Leakproof flip-top lid, BPA-free, dishwasher-safe inner sleeve.",
          "brand":{"@type":"Brand","name":"Acme"},"sku":"WIDGET-32",
          "offers":{"@type":"Offer","priceCurrency":"USD","price":"29.99","availability":"https://schema.org/InStock"}}
        </script>
      </head><body><h1>Widget</h1>
        <img src="https://cdn.shopify.com/s/files/1/0/products/widget.jpg" alt="Widget bottle">
        <p>$29.99</p>
      </body></html>`);
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

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

async function waitForAuditDone(auditID, timeoutMs = 30000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const out = sqlGet(`SELECT status::text FROM audits WHERE id='${auditID}';`)
      .split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('-') && !l.startsWith('(') && l !== 'status' && l !== '----------');
    const s = out[out.length - 1];
    if (s === 'succeeded' || s === 'failed') return s;
    await sleep(500);
  }
  throw new Error('audit did not complete: ' + auditID);
}

async function fetchMailFor(toEmail) {
  const data = await fetch('http://localhost:8025/api/v2/messages').then(r => r.json());
  const list = [];
  for (const m of (data.items || [])) {
    const to = (m.Content.Headers.To || [''])[0];
    if (!to.includes(toEmail)) continue;
    list.push({
      subject: (m.Content.Headers.Subject || [''])[0],
      bodyB64: m.MIME?.Parts?.[0]?.Body || m.Content.Body,
      raw: m.Content.Body,
      to,
    });
  }
  return list;
}

(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);
  sql(`DELETE FROM audit_jobs WHERE kind='audit_store';`);

  const fixture = await startFixtureStore();

  const stamp = Date.now().toString(36);
  const owner = { name: 'Carl Tester', email: `carl-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Carl ${stamp}` };

  const dbURL = 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable';
  const env = { DATABASE_URL: dbURL };

  console.log('starting server, worker...');
  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  const worker = spawnLogged('worker', path.join(ROOT, 'bin/worker'), ['-mode=worker'], env);
  await sleep(2000);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* retry */ }
    await sleep(200);
  }

  // 1. Sign up + verify
  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await sleep(500);
  const verifyMail = (await fetchMailFor(owner.email))[0];
  const verifyURL = verifyMail.raw.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  const userID = sqlGet(`SELECT id FROM users WHERE email='${owner.email}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // 2. Inject the store + subscribe owner to all four triggers
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme Alerts', 'connected', true, '24 hours', 'warning');`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='localhost:9999';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  sql(`INSERT INTO store_alert_subscriptions
       (tenant_id, store_id, user_id, channel, target, min_severity, enabled,
        on_new_critical, on_score_drop, on_audit_failed, on_gmc_account_change, score_drop_threshold)
       VALUES
       ('${tenantID}', '${storeID}', '${userID}', 'email', '${owner.email}', 'warning', true,
        true, true, true, false, 5);`);

  // Reset mailhog so the only emails left are alerts (and we already grabbed verification).
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });

  // 3. Login + Run audit #1 manually
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/audits"] button[type=submit]').click(),
  ]);
  // URL is /t/.../audits/{id}
  const audit1ID = page.url().split('/audits/')[1];
  console.log(`audit #1 enqueued: ${audit1ID}`);
  const s1 = await waitForAuditDone(audit1ID);
  console.log(`audit #1 status: ${s1}`);
  await sleep(1500); // give the dispatcher's goroutine time to send

  // 4. Audit #2 (same fixture → no diff change; should NOT trigger any always-send,
  //    and rate limit suppresses score_drop even if we didn't trigger one)
  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/audits"] button[type=submit]').click(),
  ]);
  const audit2ID = page.url().split('/audits/')[1];
  console.log(`audit #2 enqueued: ${audit2ID}`);
  const s2 = await waitForAuditDone(audit2ID);
  console.log(`audit #2 status: ${s2}`);
  await sleep(1500);

  // 5. Output
  console.log('\n=== alert_dispatches log ===');
  console.log(sql(`SELECT substr(audit_id::text,1,8) AS audit, trigger, channel, target, sent_at::time FROM alert_dispatches WHERE tenant_id='${tenantID}' ORDER BY sent_at;`));

  console.log('\n=== mailhog inbox for ${owner.email} ===');
  const mails = await fetchMailFor(owner.email);
  for (const m of mails) {
    console.log(`  • [${m.subject}] → ${m.to}`);
  }

  // Save the first alert email to disk so we can show it as a deliverable.
  const alertMails = mails.filter(m => /critical|score|failed|update/i.test(m.subject));
  if (alertMails.length) {
    const decoded = (() => {
      const raw = alertMails[0].raw;
      // mailhog stores quoted-printable + base64 in raw. The body is HTML —
      // for the screenshot we just need it to render in a browser, so:
      return raw;
    })();
    fs.writeFileSync(path.join(TMP, 'alert-email-raw.eml'), decoded);
    // Also extract just the HTML by trimming SMTP envelope.
    const m = decoded.match(/<!DOCTYPE html>[\s\S]+<\/html>/i);
    if (m) {
      // quoted-printable decode (rough)
      const html = m[0]
        .replace(/=\r?\n/g, '')
        .replace(/=([0-9A-Fa-f]{2})/g, (_, h) => String.fromCharCode(parseInt(h, 16)));
      fs.writeFileSync(path.join(TMP, 'alert-email.html'), html);
    }
  }

  console.log('\n=== dispatcher log lines ===');
  console.log(execSync(`grep -E '"alert_sent"|"alerts_rate_limited"|"alerts_no_triggers"' ${path.join(TMP, 'worker.log')} || true`, { encoding: 'utf8' }));

  // Render the saved HTML in the browser to capture a screenshot
  if (fs.existsSync(path.join(TMP, 'alert-email.html'))) {
    await page.goto('file://' + path.join(TMP, 'alert-email.html'), { waitUntil: 'networkidle' });
    await page.screenshot({ path: path.join(TMP, 'screenshots', '70-alert-email.png'), fullPage: true });
  }

  await browser.close();
  fixture.close();
  worker.kill('SIGTERM');
  server.kill('SIGTERM');
  await sleep(500);
  console.log(JSON.stringify({ owner: owner.email, slug, tenantID, userID, storeID, audit1ID, audit2ID }, null, 2));
})().catch(err => { console.error(err); process.exit(1); });
