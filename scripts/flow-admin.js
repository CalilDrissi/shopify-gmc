// End-to-end impersonation flow:
//   Alice signs up + verifies, owns "Acme"
//   Bob signs up + verifies, accepts Alice's invitation → member of Acme
//   Carol signs up + verifies; cmd/seed grants her super_admin
//   Carol logs in to /admin/login, enrolls TOTP, lands on /admin
//   Carol opens Acme detail, impersonates Bob, sees the red banner on /t/acme
//   Mailhog confirms Alice received the impersonation notice
//   Carol clicks "Stop impersonating" → back to admin
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const { authenticator } = require('otplib');

const BASE = process.env.BASE || 'http://localhost:8080';
const MAILHOG = process.env.MAILHOG || 'http://localhost:8025';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

async function getMailLink(toEmail, subjectPrefix, kind) {
  const data = await fetch(`${MAILHOG}/api/v2/messages`).then(r => r.json());
  for (const m of data.items) {
    const to = (m.Content.Headers.To || [''])[0];
    const subj = (m.Content.Headers.Subject || [''])[0];
    if (!to.includes(toEmail) || !subj.startsWith(subjectPrefix)) continue;
    const re = new RegExp(`http://localhost:8080/${kind}/[A-Za-z0-9_-]+`);
    const match = m.Content.Body.match(re);
    if (match) return match[0];
  }
  throw new Error(`no ${kind} mail for ${toEmail}`);
}

async function findMailTo(toEmail, subjectPrefix) {
  const data = await fetch(`${MAILHOG}/api/v2/messages`).then(r => r.json());
  for (const m of data.items) {
    const to = (m.Content.Headers.To || [''])[0];
    const subj = (m.Content.Headers.Subject || [''])[0];
    if (to.includes(toEmail) && (!subjectPrefix || subj.startsWith(subjectPrefix))) {
      return { subject: subj, body: m.Content.Body };
    }
  }
  return null;
}

async function signup(page, name, email, workspace, password) {
  await page.goto(`${BASE}/signup`, { waitUntil: 'networkidle' });
  await page.fill('input[name=name]', name);
  await page.fill('input[name=email]', email);
  await page.fill('input[name=workspace]', workspace);
  await page.fill('input[name=password]', password);
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.click('button[type=submit]'),
  ]);
}

async function verify(page, email) {
  const link = await getMailLink(email, 'Confirm', 'verify-email');
  await page.goto(link, { waitUntil: 'networkidle' });
}

async function login(page, email, password) {
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await page.fill('input[name=email]', email);
  await page.fill('input[name=password]', password);
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }),
    page.click('button[type=submit]'),
  ]);
}

async function waitForAlpine(page) {
  await page.waitForFunction(() => {
    const m = document.querySelector('.c-switcher__menu');
    if (!m) return true;
    return !m.hasAttribute('x-cloak');
  }, null, { timeout: 5000 }).catch(() => {});
}

(async () => {
  await fetch(`${MAILHOG}/api/v1/messages`, { method: 'DELETE' });
  const stamp = Date.now().toString(36);

  const owner   = { name: 'Alice Owner',  email: `alice-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Acme ${stamp}` };
  const invitee = { name: 'Bob Invitee',  email: `bob-${stamp}@example.com`,   pw: 'super-strong-pass-2026', ws: `Bobworks ${stamp}` };
  const admin   = { name: 'Carol Admin',  email: `carol-${stamp}@example.com`, pw: 'super-strong-pass-2026', ws: `Carolworks ${stamp}` };

  const browser = await chromium.launch({ headless: false });

  // ---- Onboarding: Alice + Bob via the public flow ----
  const ctxA = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pageA = await ctxA.newPage();
  await signup(pageA, owner.name, owner.email, owner.ws, owner.pw);
  await verify(pageA, owner.email);
  await login(pageA, owner.email, owner.pw);

  // Alice invites Bob.
  await pageA.click('text=Members');
  await pageA.waitForLoadState('networkidle');
  await pageA.fill('input[name=email]', invitee.email);
  await Promise.all([
    pageA.waitForNavigation({ waitUntil: 'networkidle' }),
    pageA.locator('form[action$="/invitations"] button[type=submit]').click(),
  ]);

  // Bob signs up + accepts.
  const ctxB = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pageB = await ctxB.newPage();
  await signup(pageB, invitee.name, invitee.email, invitee.ws, invitee.pw);
  await verify(pageB, invitee.email);
  await login(pageB, invitee.email, invitee.pw);
  const inviteURL = await getMailLink(invitee.email, "You're invited", 'invitations');
  await pageB.goto(inviteURL, { waitUntil: 'networkidle' });
  await Promise.all([
    pageB.waitForNavigation({ waitUntil: 'networkidle' }),
    pageB.locator('form[action$="/accept"] button[type=submit]').click(),
  ]);
  await ctxB.close();

  // ---- Carol becomes a platform admin ----
  const ctxC = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pageC = await ctxC.newPage();
  await signup(pageC, admin.name, admin.email, admin.ws, admin.pw);
  await verify(pageC, admin.email);

  // grant via cmd/seed
  execSync(`go run ./cmd/seed grant-admin ${admin.email} super`, {
    cwd: path.join(__dirname, '..'),
    env: { ...process.env, DATABASE_URL: 'postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable' },
    stdio: 'inherit',
  });

  // Visit /admin/login as Carol
  await pageC.goto(`${BASE}/admin/login`, { waitUntil: 'networkidle' });
  await pageC.screenshot({ path: path.join(OUT, '30-admin-login.png'), fullPage: true });

  await pageC.fill('input[name=email]', admin.email);
  await pageC.fill('input[name=password]', admin.pw);
  await Promise.all([
    pageC.waitForNavigation({ waitUntil: 'networkidle' }),
    pageC.click('button[type=submit]'),
  ]);
  // Should land on /admin/totp/enroll for first-time admin
  await pageC.screenshot({ path: path.join(OUT, '31-admin-totp-enroll.png'), fullPage: true });

  const secret = await pageC.locator('input[name=secret]').inputValue();
  if (!secret) throw new Error('no TOTP secret on enroll page');
  const code = authenticator.generate(secret);
  await pageC.fill('input[name=code]', code);
  await Promise.all([
    pageC.waitForNavigation({ waitUntil: 'networkidle' }),
    pageC.click('button[type=submit]'),
  ]);
  await pageC.screenshot({ path: path.join(OUT, '32-admin-dashboard.png'), fullPage: true });

  // Tenants list
  await pageC.click('text=Tenants');
  await pageC.waitForLoadState('networkidle');
  await pageC.screenshot({ path: path.join(OUT, '33-admin-tenants.png'), fullPage: true });

  // Open Acme detail
  await pageC.click(`text=${owner.ws}`);
  await pageC.waitForLoadState('networkidle');
  await pageC.screenshot({ path: path.join(OUT, '34-admin-tenant-detail.png'), fullPage: true });

  // Pick Bob as the impersonation target, type a reason, submit
  await pageC.locator('select[name=user_id]').selectOption({ label: `${invitee.email} (member)` });
  await pageC.fill('input[name=reason]', 'Demo: investigating reported audit issue');
  await Promise.all([
    pageC.waitForNavigation({ waitUntil: 'networkidle' }),
    pageC.locator('form[action$="/impersonate"] button[type=submit]').click(),
  ]);
  await waitForAlpine(pageC);
  await pageC.screenshot({ path: path.join(OUT, '35-impersonating-tenant.png'), fullPage: true });

  // Verify Mailhog received the impersonation notice to Alice
  const note = await findMailTo(owner.email, 'Account access notice');
  if (!note) console.error('WARN: no impersonation notice email to', owner.email);

  // Stop impersonating
  await Promise.all([
    pageC.waitForNavigation({ waitUntil: 'networkidle' }),
    pageC.locator('form[action$="/impersonation/stop"] button[type=submit]').click(),
  ]);
  await pageC.screenshot({ path: path.join(OUT, '36-after-stop.png'), fullPage: true });

  // Audit log
  await pageC.goto(`${BASE}/admin/audit-log`, { waitUntil: 'networkidle' });
  await pageC.screenshot({ path: path.join(OUT, '37-admin-audit-log.png'), fullPage: true });

  // Settings
  await pageC.goto(`${BASE}/admin/settings`, { waitUntil: 'networkidle' });
  await pageC.screenshot({ path: path.join(OUT, '38-admin-settings.png'), fullPage: true });

  console.log(JSON.stringify({ owner: owner.email, invitee: invitee.email, admin: admin.email }, null, 2));
  await browser.close();
})().catch(err => { console.error(err); process.exit(1); });
