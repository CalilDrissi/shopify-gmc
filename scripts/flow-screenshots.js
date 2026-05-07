// Drives the public auth flow in headed Chromium and snapshots each page.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'http://localhost:8080';
const MAILHOG = process.env.MAILHOG || 'http://localhost:8025';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: false });
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();

  // Clear mailhog so we can find the verification email deterministically.
  await fetch(`${MAILHOG}/api/v1/messages`, { method: 'DELETE' });

  // Use a fresh email each run.
  const stamp = Date.now().toString(36);
  const email = `bob-${stamp}@example.com`;
  const slug = `northwind-${stamp}`;

  // 1. Landing
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '01-landing.png'), fullPage: true });

  // 2. Pricing
  await page.goto(`${BASE}/pricing`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '02-pricing.png'), fullPage: true });

  // 3. Features
  await page.goto(`${BASE}/features`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '03-features.png'), fullPage: true });

  // 4. Signup form
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '04-signup.png'), fullPage: true });

  // 5. Submit signup
  await page.fill('input[name=name]', 'Bob Builder');
  await page.fill('input[name=email]', email);
  await page.fill('input[name=workspace]', `Northwind ${stamp}`);
  await page.fill('input[name=password]', 'super-strong-passphrase-2026');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.click('button[type=submit]'),
  ]);
  await page.screenshot({ path: path.join(OUT, '05-verify-pending.png'), fullPage: true });

  // 6. Pull the verification URL from Mailhog
  const mh = await fetch(`${MAILHOG}/api/v2/messages`).then(r => r.json());
  const body = mh.items[0].Content.Body;
  const match = body.match(/http:\/\/localhost:8080\/verify-email\/[A-Za-z0-9_-]+/);
  if (!match) throw new Error('verification URL not found in mailhog');
  await page.goto(match[0], { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '06-email-confirmed.png'), fullPage: true });

  // 7. Login form
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '07-login.png'), fullPage: true });

  // 8. Submit login
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', 'super-strong-passphrase-2026');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.click('button[type=submit]'),
  ]);
  await page.screenshot({ path: path.join(OUT, '08-dashboard.png'), fullPage: true });

  // 9. Forgot password page (just so we have a screenshot)
  await page.goto(`${BASE}/forgot-password`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '09-forgot-password.png'), fullPage: true });

  // 10. Error page (404)
  await page.goto(`${BASE}/no-such-route`, { waitUntil: 'networkidle' });
  await page.screenshot({ path: path.join(OUT, '10-not-found.png'), fullPage: true });

  // 11. Login with bad password — show the critical banner
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', 'wrong-password');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.click('button[type=submit]'),
  ]);
  await page.screenshot({ path: path.join(OUT, '11-login-failed.png'), fullPage: true });

  console.log(JSON.stringify({ email, slug }, null, 2));
  await browser.close();
})().catch(err => { console.error(err); process.exit(1); });
