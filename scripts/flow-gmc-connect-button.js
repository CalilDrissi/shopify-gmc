// Quick visual check that the GMC card renders + the connect button is
// owner-only. We don't actually click through to Google (no real creds
// in this run), just screenshot the disconnected state.

const { chromium } = require('playwright');
const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const BASE = 'http://localhost:8080';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

const sql = (s) => execSync(`docker exec gmcauditor-postgres psql -U gmc -d gmcauditor -c "${s}"`, { encoding: 'utf8' });
const sqlGet = (s) => sql(s).trim();

(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);

  const stamp = Date.now().toString(36);
  const owner = { name: 'Gina Gmc', email: `gina-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Gina ${stamp}` };

  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
  const page = await ctx.newPage();

  // Sign up
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  // Verify
  await new Promise(r => setTimeout(r, 500));
  const mailItems = (await fetch('http://localhost:8025/api/v2/messages').then(r => r.json())).items;
  const mail = mailItems.find(m => (m.Content.Headers.To || [''])[0].includes(owner.email));
  const verifyURL = mail.Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  // Upgrade plan so the Connect button is shown rather than the upsell.
  sql(`UPDATE tenants SET plan='agency'::plan_tier WHERE id='${tenantID}';`);
  // Inject a fixture store.
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme GMC', 'connected', false, '7 days', 'warning');`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='localhost:9999';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Login
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);

  await page.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '90-gmc-connect-card.png'), fullPage: true });

  await browser.close();
  console.log(JSON.stringify({ owner: owner.email, slug, storeID }, null, 2));
})().catch(err => { console.error(err); process.exit(1); });
