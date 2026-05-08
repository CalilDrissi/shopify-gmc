// Demo for scheduler + differ:
//   1. Sign up + verify a fresh user
//   2. Inject a Shopify-fixture store with a 5-second monitor cadence + next_audit_at=now
//   3. Start a fresh server + worker + scheduler (5s tick)
//   4. Wait for the scheduler to claim the store and enqueue 2 scheduled audits
//   5. Show: scheduler logs, audit_diffs row from db, audits-list

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

(async () => {
  // Reset Mailhog so the run's only emails are this demo's.
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });

  // Wipe stale localhost:9999 stores from prior runs so the scheduler claims
  // this demo's store cleanly. Leftovers thrash the worker against an
  // unbound port.
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);
  sql(`DELETE FROM audit_jobs WHERE kind='audit_store';`);

  const fixture = await startFixtureStore();

  const stamp = Date.now().toString(36);
  const owner = { name: 'Mona Mon', email: `mona-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Mona ${stamp}` };

  const dbURL = 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable';
  const env = { DATABASE_URL: dbURL };

  // 1. Sign up via the running server with the fastest path possible: hit
  //    /signup, capture the verify mail, click link.
  const { chromium } = require('playwright');
  // Boot a fresh server + worker + scheduler so the scheduler's logs in
  // /tmp/scheduler.log show only this demo's claims.
  console.log('starting server, worker, scheduler...');
  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  const worker = spawnLogged('worker', path.join(ROOT, 'bin/worker'), ['-mode=worker'], env);
  await sleep(2000);
  // Server up?
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* retry */ }
    await sleep(200);
  }

  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  // Verify
  await sleep(500);
  const mail = await fetch('http://localhost:8025/api/v2/messages').then(r => r.json());
  const m = mail.items.find(it => (it.Content.Headers.To || [''])[0].includes(owner.email));
  const verifyURL = m.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });

  // Slug + tenant ID from db
  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // 2. Inject a fixture store with monitor_enabled=true, monitor_frequency=5s,
  //    next_audit_at=now (so the scheduler claims it on its first tick).
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold, next_audit_at)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme Monitored', 'connected', true, '5 seconds', 'warning', now() - interval '1 second');`);

  // 3. Start the scheduler with a 2s tick so we get two scheduled audits in this demo.
  console.log('starting scheduler...');
  const scheduler = spawnLogged('scheduler', path.join(ROOT, 'bin/scheduler'), ['-mode=scheduler'], { ...env, SCHEDULER_TICK: '2s' });

  // 4. Wait for two completed scheduled audits (status=succeeded, trigger=scheduled).
  console.log('waiting for two scheduled audits to complete...');
  let completed = 0, lastIDs = [];
  for (let i = 0; i < 60; i++) { // ~60s budget
    await sleep(1000);
    const out = sqlGet(`SELECT count(*) FROM audits WHERE tenant_id='${tenantID}' AND trigger='scheduled' AND status='succeeded';`);
    const n = parseInt(out.split('\n').filter(l => /^\s*\d/.test(l))[0].trim(), 10) || 0;
    if (n !== completed) {
      completed = n;
      console.log(`  completed scheduled audits: ${completed}`);
    }
    if (completed >= 2) break;
  }
  if (completed < 2) {
    console.error('timed out waiting for 2 scheduled audits');
  }

  // Login and screenshot the audits list to demo trigger=scheduled.
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await page.goto(`${BASE}/t/${slug}/audits`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(TMP, 'screenshots', '60-audits-list-scheduled.png'), fullPage: true });

  // 5. Print the latest audit_diffs row.
  console.log('\n=== latest audit_diffs row ===');
  const diffOut = sql(`SELECT id, audit_id, previous_audit_id, new_issue_count, resolved_issue_count, unchanged_count, new_critical_count, prev_score, new_score, score_delta FROM audit_diffs WHERE tenant_id='${tenantID}' ORDER BY created_at DESC LIMIT 2;`);
  console.log(diffOut);

  // Stop everything cleanly so the parent script returns.
  await browser.close();
  fixture.close();
  scheduler.kill('SIGTERM');
  worker.kill('SIGTERM');
  server.kill('SIGTERM');
  await sleep(500);

  console.log('\n=== scheduler.log (last 30 lines) ===');
  console.log(execSync(`tail -n 30 ${path.join(TMP, 'scheduler.log')}`, { encoding: 'utf8' }));

  console.log(JSON.stringify({ owner: owner.email, slug, tenantID, completed }, null, 2));
})().catch(err => { console.error(err); process.exit(1); });
