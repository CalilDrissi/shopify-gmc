// End-to-end audit flow:
//   1. Sign up + verify a fresh user
//   2. Inject a Shopify-fixture store directly into the DB (skip the URL-ping form)
//   3. Click "Run audit" — server enqueues, worker picks up
//   4. Watch the live progress page swap each stage in via HTMX polling
//   5. After it finishes, screenshot the completed view
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'http://localhost:8080';
const MAILHOG = process.env.MAILHOG || 'http://localhost:8025';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

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
          <url><loc>http://localhost:9999/products/gadget</loc></url>
          <url><loc>http://localhost:9999/policies/refund-policy</loc></url>
          <url><loc>http://localhost:9999/policies/privacy-policy</loc></url>
          <url><loc>http://localhost:9999/pages/about</loc></url>
        </urlset>`);
      }
      if (u === '/') return html(`<!DOCTYPE html><html><head>
          <title>Acme Goods</title>
          <link rel="canonical" href="http://localhost:9999/">
          <link rel="stylesheet" href="https://cdn.shopify.com/s/themes/dawn/style.css">
          <script>window.Shopify={theme:{name:'Dawn'}};</script>
        </head><body>
          <h1>Acme Goods</h1>
          <a href="/products/widget">Widget</a>
          <a href="/products/gadget">Gadget</a>
          <footer>
            <p>123 Market Street · Brooklyn NY 11201</p>
            <p><a href="mailto:hello@acme.example">hello@acme.example</a> · <a href="tel:+15551234567">+1 (555) 123-4567</a></p>
            <a href="/policies/refund-policy">Returns</a>
            <a href="/policies/privacy-policy">Privacy</a>
            <a href="/pages/about">About</a>
          </footer>
        </body></html>`);
      if (u === '/products/widget' || u === '/products/gadget') {
        const name = u === '/products/widget' ? 'Widget' : 'Gadget';
        const price = u === '/products/widget' ? '29.99' : '49.50';
        return html(`<!DOCTYPE html><html><head>
          <title>${name}</title>
          <link rel="canonical" href="http://localhost:9999${u}">
          <script>window.Shopify={theme:{name:'Dawn'}};</script>
          <script type="application/ld+json">
          {"@context":"https://schema.org/","@type":"Product",
            "name":"${name}",
            "image":"https://cdn.shopify.com/s/files/1/0/products/${name}.jpg",
            "description":"The ${name} is a vacuum-insulated 32oz stainless steel water bottle. Triple-wall construction keeps drinks cold for 24 hours, hot for 12. Leakproof flip-top lid, BPA-free, dishwasher-safe inner sleeve.",
            "brand":{"@type":"Brand","name":"Acme"},"sku":"${name.toUpperCase()}-32",
            "offers":{"@type":"Offer","priceCurrency":"USD","price":"${price}","availability":"https://schema.org/InStock"}
          }
          </script>
        </head><body>
          <h1>${name}</h1>
          <img src="https://cdn.shopify.com/s/files/1/0/products/${name}.jpg" alt="${name} stainless steel bottle in matte black">
          <p>$${price}</p>
        </body></html>`);
      }
      if (u === '/policies/refund-policy') return html('<html><body><h1>Returns</h1><p>30-day returns.</p></body></html>');
      if (u === '/policies/privacy-policy') return html("<html><body><h1>Privacy</h1><p>We don't sell your data.</p></body></html>");
      if (u === '/pages/about') return html(`<html><body><h1>About</h1>
        <p>Acme Goods was founded in 2018 by Dana Park, a textile designer who couldn't find a single small-batch supplier of organic-cotton aprons in the Bay Area. We started with eight aprons sewn in Dana's garage and have grown into a 12-person studio in Oakland that ships to all 50 states and 14 countries.</p>
      </body></html>`);
      res.writeHead(404); res.end('not found');
    });
    server.listen(9999, '127.0.0.1', () => resolve(server));
  });
}

async function getMailLink(toEmail) {
  const data = await fetch(`${MAILHOG}/api/v2/messages`).then(r => r.json());
  for (const m of data.items) {
    const to = (m.Content.Headers.To || [''])[0];
    if (!to.includes(toEmail)) continue;
    const match = m.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/);
    if (match) return match[0];
  }
  throw new Error('verify mail not found');
}

const sql = (s) => execSync(`docker exec gmcauditor-postgres psql -U gmc -d gmcauditor -c "${s}"`, { encoding: 'utf8' });
const sqlGet = (s) => sql(s).trim();

(async () => {
  await fetch(`${MAILHOG}/api/v1/messages`, { method: 'DELETE' });
  const fixture = await startFixtureStore();

  const stamp = Date.now().toString(36);
  const owner = { name: 'Alice Owner', email: `alice-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Acme ${stamp}` };

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
  await page.goto(await getMailLink(owner.email), { waitUntil: 'networkidle' });

  // Inject the fixture store directly (bypass form's pingURL).
  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold) VALUES ('${tenantID}', 'localhost:9999', 'Acme Fixture Store', 'connected', true, '24 hours', 'warning');`);

  // Login
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  // Stores list
  await page.click('text=Stores');
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: path.join(OUT, '40-stores-list.png'), fullPage: true });

  // Open the store, screenshot detail, click Run audit
  await page.click('text=Acme Fixture Store');
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: path.join(OUT, '41-store-detail.png'), fullPage: true });

  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.locator('form[action$="/audits"] button[type=submit]').click(),
  ]);
  await page.screenshot({ path: path.join(OUT, '42-audit-queued.png'), fullPage: true });

  // Watch for status to change. Try to capture "running" mid-flight.
  await page.waitForFunction(() => {
    const el = document.querySelector('.audit-progress');
    if (!el) return false;
    const s = el.getAttribute('data-status');
    return s === 'running' || s === 'succeeded' || s === 'failed';
  }, null, { timeout: 30000 });
  if ((await page.locator('.audit-progress').getAttribute('data-status')) === 'running') {
    await page.screenshot({ path: path.join(OUT, '43-audit-running.png'), fullPage: true });
  }

  await page.waitForFunction(() => {
    const el = document.querySelector('.audit-progress');
    return el && (el.getAttribute('data-status') === 'succeeded' || el.getAttribute('data-status') === 'failed');
  }, null, { timeout: 60000 });
  // After the report is rendered (gauge + grouped issues), screenshot top + bottom.
  await page.waitForSelector('.c-report', { timeout: 5000 });
  await page.screenshot({ path: path.join(OUT, '44-audit-done.png'), fullPage: false });
  await page.screenshot({ path: path.join(OUT, '46-audit-report-full.png'), fullPage: true });

  // Expand the first "How to apply" panel to demonstrate the Alpine collapse.
  const firstHowTo = page.locator('.c-issue__how-toggle').first();
  if (await firstHowTo.count()) {
    await firstHowTo.click();
    await page.waitForTimeout(150);
    const issueCard = page.locator('.c-issue').first();
    await issueCard.scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(OUT, '47-issue-expanded.png'), fullPage: false });
  }

  // Click the "Copy" button on the AI suggestion to verify Alpine wiring.
  const firstCopy = page.locator('.c-issue__row--ai .c-button').first();
  if (await firstCopy.count()) {
    await firstCopy.click();
    await page.waitForTimeout(200);
    await page.screenshot({ path: path.join(OUT, '48-issue-copied.png'), fullPage: false });
  }

  // Mark the first issue as resolved (POST → redirect with #anchor).
  const firstResolve = page.locator('form[action*="/resolve"] button[type=submit]').first();
  if (await firstResolve.count()) {
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'networkidle' }),
      firstResolve.click(),
    ]);
    await page.waitForSelector('.c-issue__resolved');
    await page.locator('.c-issue__resolved').first().scrollIntoViewIfNeeded();
    await page.screenshot({ path: path.join(OUT, '49-issue-resolved.png'), fullPage: false });
  }

  // Audits list
  await page.goto(`${BASE}/t/${slug}/audits`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '45-audits-list.png'), fullPage: true });

  // PDF: download via the same authenticated browser context.
  const pdfPath = path.join(OUT, '..', 'audit-report.pdf');
  await page.goto(`${BASE}/t/${slug}/audits`, { waitUntil: 'networkidle' });
  const cookieHeader = (await ctx.cookies()).map(c => `${c.name}=${c.value}`).join('; ');
  // Find the audit ID from the audits list.
  const auditID = await page.locator('a[href*="/audits/"]').first().getAttribute('href');
  const pdfURL = `${BASE}${auditID}/report.pdf`;
  console.log('downloading PDF from', pdfURL);
  const resp = await fetch(pdfURL, { headers: { Cookie: cookieHeader } });
  if (!resp.ok) throw new Error(`pdf fetch failed: ${resp.status}`);
  const buf = Buffer.from(await resp.arrayBuffer());
  fs.writeFileSync(pdfPath, buf);
  console.log('pdf saved', pdfPath, buf.length, 'bytes');

  console.log(JSON.stringify({ owner: owner.email, ws: owner.ws, slug, tenantID }, null, 2));
  await browser.close();
  fixture.close();
})().catch(err => { console.error(err); process.exit(1); });
