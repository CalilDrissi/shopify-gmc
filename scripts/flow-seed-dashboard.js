// Login as the seeded users + screenshot what they see.

const { spawn, execSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const BASE = 'http://localhost:8080';
const ROOT = path.join(__dirname, '..');
const TMP = path.join(ROOT, 'tmp');
const OUT = path.join(TMP, 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

function spawnLogged(name, bin, args, env) {
  const out = fs.createWriteStream(path.join(TMP, name + '.log'));
  const p = spawn(bin, args, { env: { ...process.env, ...env }, stdio: ['ignore', 'pipe', 'pipe'] });
  p.stdout.pipe(out, { end: false });
  p.stderr.pipe(out, { end: false });
  return p;
}
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

(async () => {
  // Re-seed in case the DB drifted from earlier turns.
  execSync(`go run ./cmd/seed all`, {
    cwd: ROOT,
    env: { ...process.env, DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable',
      SEED_ADMIN_EMAIL: 'admin@gmcauditor.local', SEED_ADMIN_PASSWORD: 'super-strong-pass-2026' },
    stdio: 'inherit',
  });

  const env = { DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable' };
  const server = spawnLogged('server', path.join(ROOT, 'bin/server'), [], env);
  await sleep(2500);
  for (let i = 0; i < 20; i++) {
    try { const r = await fetch(BASE + '/'); if (r.ok) break; } catch { /* */ }
    await sleep(200);
  }

  const { chromium } = require('playwright');
  const browser = await chromium.launch({ headless: false });

  const tours = [
    { who: 'sarah@sarahsshop.example',          slug: 'sarahs-shop',       label: 'B1-sarah-dashboard' },
    { who: 'alex@growthcollective.example',     slug: 'growth-collective', label: 'B2-alex-dashboard' },
  ];
  for (const tour of tours) {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
    const page = await ctx.newPage();
    await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
    await page.fill('input[name=email]', tour.who);
    await page.fill('input[name=password]', 'super-strong-pass-2026');
    await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
    await page.goto(`${BASE}/t/${tour.slug}`, { waitUntil: 'networkidle' });
    await page.screenshot({ path: path.join(OUT, tour.label + '.png'), fullPage: true });

    await page.goto(`${BASE}/t/${tour.slug}/stores`, { waitUntil: 'networkidle' });
    await page.screenshot({ path: path.join(OUT, tour.label.replace('dashboard', 'stores') + '.png'), fullPage: true });

    await page.goto(`${BASE}/t/${tour.slug}/audits`, { waitUntil: 'networkidle' });
    await page.screenshot({ path: path.join(OUT, tour.label.replace('dashboard', 'audits') + '.png'), fullPage: true });

    await ctx.close();
  }

  await browser.close();
  server.kill('SIGTERM');
  await sleep(500);
})().catch(err => { console.error(err); process.exit(1); });
