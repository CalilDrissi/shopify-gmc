// Turn-3 GMC end-to-end demo:
//   1. Boot a mock Google server (token + Content API endpoints)
//   2. Sign up tenant owner, upgrade to agency, seed an encrypted GMC connection
//   3. Run an audit (twice — second one shows up in the timeline)
//   4. Screenshot:
//      a) Tenant dashboard with the GMC summary card
//      b) Audit detail with the new tabbed view (Crawler / GMC / Timeline)
//   5. Promote a separate user to platform admin via cmd/seed
//   6. TOTP-enroll, log in, screenshot /admin/gmc

const { execSync, spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { authenticator } = require('otplib');

const BASE = 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
const OUT = path.join(TMP, 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

// ---------------------------------------------------------------------------
// Mock Google server
// ---------------------------------------------------------------------------
function startMockGoogle(merchantID) {
  return new Promise((resolve) => {
    const requests = [];
    const server = http.createServer((req, res) => {
      const u = req.url || '';
      requests.push({ method: req.method, url: u });
      const json = (obj) => { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(obj)); };
      if (u === '/token' && req.method === 'POST') {
        return json({ access_token: 'mock-access-' + Date.now(), token_type: 'Bearer', expires_in: 3600,
          scope: 'https://www.googleapis.com/auth/content' });
      }
      if (u === `/content/v2.1/${merchantID}/accountstatuses/${merchantID}`) {
        return json({
          merchantId: merchantID,
          websiteClaimed: false,
          accountLevelIssues: [
            { id: 'editorial_review', title: 'Editorial review pending', severity: 'error',
              detail: 'Some products are awaiting editorial review.' },
            { id: 'merchant_quality_low', title: 'Improve shopping experience score', severity: 'suggestion',
              detail: 'Your shopping experience score is below average.' },
          ],
          products: [{ channel: 'online', country: 'US', active: 18, pending: 1, disapproved: 1, expiring: 0 }],
        });
      }
      if (u.startsWith(`/content/v2.1/${merchantID}/productstatuses`)) {
        return json({
          resources: [
            { productId: 'online:en:US:WIDGET-32', title: 'Widget 32oz', link: 'http://localhost:9999/products/widget',
              destinationStatuses: [{ destination: 'Shopping', status: 'disapproved', disapprovedCountries: ['US'] }],
              itemLevelIssues: [
                { code: 'landing_page_price_mismatch', description: 'Mismatch between price on landing page and feed',
                  detail: 'Feed says $29.99; landing page renders $24.99.', destination: 'Shopping',
                  attributeName: 'price' },
                { code: 'image_link_broken', description: 'Image URL returns an error',
                  detail: 'Got 404 fetching the image.', destination: 'Shopping', attributeName: 'image_link' },
              ] },
            { productId: 'online:en:US:GADGET-1', title: 'Gadget', link: 'http://localhost:9999/products/gadget',
              destinationStatuses: [{ destination: 'Shopping', status: 'pending' }],
              itemLevelIssues: [
                { code: 'missing_gtin', description: 'Missing GTIN', detail: 'Without GTIN your products may show up less often.',
                  destination: 'Shopping', attributeName: 'gtin' },
              ] },
            { productId: 'online:en:US:THING-7', title: 'Thing', link: 'http://localhost:9999/products/thing',
              destinationStatuses: [{ destination: 'Shopping', status: 'approved' }], itemLevelIssues: [] },
          ],
        });
      }
      if (u === `/content/v2.1/${merchantID}/datafeedstatuses`) {
        return json({
          resources: [
            { datafeedId: '1001', country: 'US', language: 'en', processingStatus: 'success',
              itemsTotal: 20, itemsValid: 20 },
            { datafeedId: '1002', country: 'US', language: 'en', processingStatus: 'failure',
              itemsTotal: 0, itemsValid: 0,
              errors: [{ code: 'fetch_failure', count: 1, message: 'Unable to fetch feed file (HTTP 404).' }] },
          ],
        });
      }
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'not found', path: u }));
    });
    server.listen(0, '127.0.0.1', () => resolve({ server, port: server.address().port, requests }));
  });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
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

function encryptAESGCM(keyB64, plaintext) {
  const key = Buffer.from(keyB64, 'base64');
  const nonce = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', key, nonce);
  const enc = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  const tag = cipher.getAuthTag();
  return Buffer.concat([nonce, enc, tag]);
}

async function fetchMailFor(email) {
  const data = await fetch('http://localhost:8025/api/v2/messages').then(r => r.json());
  return (data.items || []).filter(m => ((m.Content.Headers.To || [''])[0] || '').includes(email));
}

async function signupAndVerify(page, name, email, ws, pw) {
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', name);
  await page.fill('input[name=email]', email);
  await page.fill('input[name=workspace]', ws);
  await page.fill('input[name=password]', pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await sleep(500);
  const mails = await fetchMailFor(email);
  const verifyURL = mails[0].Content.Body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/)[0];
  await page.goto(verifyURL, { waitUntil: 'networkidle' });
}

async function login(page, email, pw) {
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
}

// ---------------------------------------------------------------------------
// Demo
// ---------------------------------------------------------------------------
(async () => {
  await fetch('http://localhost:8025/api/v1/messages', { method: 'DELETE' });
  sql(`DELETE FROM stores WHERE shop_domain='localhost:9999';`);
  sql(`DELETE FROM audit_jobs WHERE kind='audit_store';`);

  const merchantID = '111222333';
  const mock = await startMockGoogle(merchantID);
  console.log(`mock Google listening on http://127.0.0.1:${mock.port}`);

  const settingsKey = crypto.randomBytes(32).toString('base64');
  const env = {
    DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable',
    GOOGLE_OAUTH_CLIENT_ID:     'mock-client-id.apps.googleusercontent.com',
    GOOGLE_OAUTH_CLIENT_SECRET: 'mock-client-secret',
    GOOGLE_OAUTH_REDIRECT_URL:  'http://localhost:8080/oauth/google/callback',
    GOOGLE_OAUTH_TOKEN_URL:     `http://127.0.0.1:${mock.port}/token`,
    GOOGLE_OAUTH_AUTH_URL:      `http://127.0.0.1:${mock.port}/auth`,
    GMC_BASE_URL:               `http://127.0.0.1:${mock.port}/content/v2.1/`,
    SETTINGS_ENCRYPTION_KEY:    settingsKey,
  };

  // Tiny localhost:9999 fixture for the crawler stage
  const fixture = await new Promise((resolve) => {
    const s = http.createServer((req, res) => {
      if (req.url === '/robots.txt') { res.writeHead(200, { 'Content-Type': 'text/plain' }); return res.end('User-agent: *\nAllow: /\n'); }
      res.writeHead(200, { 'Content-Type': 'text/html' });
      res.end(`<!DOCTYPE html><html><head><title>Acme</title>
        <link rel="stylesheet" href="https://cdn.shopify.com/s/themes/dawn/style.css">
        <script>window.Shopify={theme:{name:'Dawn'}};</script>
      </head><body><h1>Acme</h1>
        <footer><p>1 St · NY</p><p>hello@acme.example</p></footer></body></html>`);
    });
    s.listen(9999, '127.0.0.1', () => resolve(s));
  });

  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  const worker = spawnLogged('worker', path.join(ROOT, 'bin/worker'), ['-mode=worker'], env);
  await sleep(2500);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* */ }
    await sleep(200);
  }

  // ---- Tenant owner side ----
  const stamp = Date.now().toString(36);
  const owner = { name: 'Greg GMC', email: `greg-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Greg ${stamp}` };
  const admin = { name: 'Aly Admin', email: `aly-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `AlyOps ${stamp}` };

  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });
  const ownerCtx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const ownerPage = await ownerCtx.newPage();

  await signupAndVerify(ownerPage, owner.name, owner.email, owner.ws, owner.pw);
  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  const tenantID = sqlGet(`SELECT id FROM tenants WHERE slug='${slug}';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();
  sql(`UPDATE tenants SET plan='agency'::plan_tier WHERE id='${tenantID}';`);
  sql(`INSERT INTO stores (tenant_id, shop_domain, display_name, status, monitor_enabled, monitor_frequency, monitor_alert_threshold)
       VALUES ('${tenantID}', 'localhost:9999', 'Acme E2E', 'connected', false, '7 days', 'warning');`);
  const storeID = sqlGet(`SELECT id FROM stores WHERE tenant_id='${tenantID}' AND shop_domain='localhost:9999';`).split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Seed a GMC connection (encrypted refresh token via real cipher).
  const enc = encryptAESGCM(settingsKey, 'mock-refresh-token-' + stamp);
  sql(`INSERT INTO store_gmc_connections
       (tenant_id, store_id, merchant_id, account_email,
        refresh_token_encrypted, token_nonce, token_expires_at,
        status, scope)
       VALUES ('${tenantID}', '${storeID}', '${merchantID}', 'greg+gmc@example.com',
               '\\\\x${enc.toString('hex')}'::bytea, ''::bytea, now() + interval '1 hour',
               'active'::gmc_connection_status, 'https://www.googleapis.com/auth/content');`);

  // Run two audits so the timeline has two entries.
  await login(ownerPage, owner.email, owner.pw);
  for (let i = 0; i < 2; i++) {
    await ownerPage.goto(`${BASE}/t/${slug}/stores/${storeID}`, { waitUntil: 'networkidle' });
    await Promise.all([
      ownerPage.waitForNavigation({ waitUntil: 'networkidle' }),
      ownerPage.locator('form[action$="/audits"] button[type=submit]').click(),
    ]);
    await ownerPage.waitForFunction(() =>
      document.querySelector('.audit-progress')?.getAttribute('data-status') === 'succeeded',
      null, { timeout: 60000 });
    await sleep(500);
  }
  const lastAuditID = sqlGet(`SELECT id FROM audits WHERE store_id='${storeID}' AND status='succeeded' ORDER BY created_at DESC LIMIT 1;`)
    .split('\n').filter(l => l.match(/^[ ]*[0-9a-f]{8}-/))[0].trim();

  // Dashboard with GMC summary card
  await ownerPage.goto(`${BASE}/t/${slug}`, { waitUntil: 'networkidle' });
  await ownerPage.screenshot({ path: path.join(OUT, '92-dashboard-gmc-summary.png'), fullPage: true });

  // Tabbed audit detail — crawler tab (default)
  await ownerPage.goto(`${BASE}/t/${slug}/audits/${lastAuditID}`, { waitUntil: 'networkidle' });
  await ownerPage.locator('.c-tabs__tab').first().scrollIntoViewIfNeeded();
  await ownerPage.screenshot({ path: path.join(OUT, '93-audit-tab-crawler.png'), fullPage: true });

  // Click the GMC tab
  await ownerPage.locator('.c-tabs__tab').nth(1).click();
  await sleep(150);
  await ownerPage.screenshot({ path: path.join(OUT, '94-audit-tab-gmc.png'), fullPage: true });

  // Click the Timeline tab
  await ownerPage.locator('.c-tabs__tab').nth(2).click();
  await sleep(150);
  await ownerPage.screenshot({ path: path.join(OUT, '95-audit-tab-timeline.png'), fullPage: true });

  // ---- Platform admin side ----
  const adminCtx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const adminPage = await adminCtx.newPage();

  await signupAndVerify(adminPage, admin.name, admin.email, admin.ws, admin.pw);
  execSync(`go run ./cmd/seed grant-admin ${admin.email} super`, {
    cwd: ROOT, env: { ...process.env, DATABASE_URL: env.DATABASE_URL }, stdio: 'inherit',
  });

  await adminPage.goto(`${BASE}/admin/login`, { waitUntil: 'networkidle' });
  await adminPage.fill('input[name=email]', admin.email);
  await adminPage.fill('input[name=password]', admin.pw);
  await Promise.all([adminPage.waitForNavigation({ waitUntil: 'networkidle' }), adminPage.click('button[type=submit]')]);

  // TOTP enroll
  const secret = await adminPage.locator('input[name=secret]').inputValue();
  await adminPage.fill('input[name=code]', authenticator.generate(secret));
  await Promise.all([adminPage.waitForNavigation({ waitUntil: 'networkidle' }), adminPage.click('button[type=submit]')]);

  // /admin/gmc
  await adminPage.goto(`${BASE}/admin/gmc`, { waitUntil: 'networkidle' });
  await adminPage.screenshot({ path: path.join(OUT, '96-admin-gmc.png'), fullPage: true });

  // Force a sync error to populate the failed-syncs panel: temporarily nuke
  // the access_token endpoint of the mock + trigger a refresh cycle.
  // Easier: manipulate last_sync_status directly to demo the panel.
  sql(`UPDATE store_gmc_connections SET last_sync_status='error', last_error_message='HTTP 429 — rate limited by Google' WHERE store_id='${storeID}';`);
  await adminPage.goto(`${BASE}/admin/gmc`, { waitUntil: 'networkidle' });
  await adminPage.screenshot({ path: path.join(OUT, '97-admin-gmc-with-failures.png'), fullPage: true });

  console.log('\n=== summary ===');
  console.log(JSON.stringify({ owner: owner.email, slug, storeID, lastAuditID, admin: admin.email, merchantID }, null, 2));

  await browser.close();
  fixture.close();
  mock.server.close();
  worker.kill('SIGTERM');
  server.kill('SIGTERM');
  await sleep(500);
})().catch(err => { console.error(err); process.exit(1); });
