// Drives the full turn-2 tenant flow:
//   user A: signup → verify → login → land on dashboard → open members → invite user B
//   user B: signup (separate workspace) → verify → login → open invitation link → accept
//   B then ends up inside A's workspace via the tenant switcher.
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'http://localhost:8080';
const MAILHOG = process.env.MAILHOG || 'http://localhost:8025';
const OUT = path.join(__dirname, '..', 'tmp', 'screenshots');
fs.mkdirSync(OUT, { recursive: true });

async function getMailLink(toEmail, subjectPrefix, kind) {
  const data = await fetch(`${MAILHOG}/api/v2/messages`).then(r => r.json());
  for (const m of data.items) {
    const subj = (m.Content.Headers.Subject || [''])[0];
    const to = (m.Content.Headers.To || [''])[0];
    if (!to.includes(toEmail) || !subj.startsWith(subjectPrefix)) continue;
    const body = m.Content.Body;
    const re = new RegExp(`http://localhost:8080/${kind}/[A-Za-z0-9_-]+`);
    const match = body.match(re);
    if (match) return match[0];
  }
  throw new Error(`no ${kind} mail for ${toEmail}`);
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
    const menu = document.querySelector('.c-switcher__menu');
    if (!menu) return true;
    return !menu.hasAttribute('x-cloak');
  }, null, { timeout: 5000 });
}

(async () => {
  await fetch(`${MAILHOG}/api/v1/messages`, { method: 'DELETE' });
  const stamp = Date.now().toString(36);

  const owner   = { name: 'Alice Owner',  email: `alice-${stamp}@example.com`,  pw: 'super-strong-pass-2026', ws: `Acme ${stamp}` };
  const invitee = { name: 'Bob Invitee',  email: `bob-${stamp}@example.com`,    pw: 'super-strong-pass-2026', ws: `Bobworks ${stamp}` };

  const browser = await chromium.launch({ headless: false });

  // ----- Owner context -----
  const ctxA = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pageA = await ctxA.newPage();
  await signup(pageA, owner.name, owner.email, owner.ws, owner.pw);
  await verify(pageA, owner.email);
  await login(pageA, owner.email, owner.pw);
  await waitForAlpine(pageA); await pageA.screenshot({ path: path.join(OUT, '20-owner-dashboard.png'), fullPage: true });

  // Open members page
  await pageA.click('text=Members');
  await pageA.waitForLoadState('networkidle');
  await waitForAlpine(pageA); await pageA.screenshot({ path: path.join(OUT, '21-members-empty.png'), fullPage: true });

  // Invite user B
  await pageA.fill('input[name=email]', invitee.email);
  await Promise.all([
    pageA.waitForNavigation({ waitUntil: 'networkidle' }),
    pageA.locator('form[action$="/invitations"] button[type=submit]').click(),
  ]);
  await waitForAlpine(pageA); await pageA.screenshot({ path: path.join(OUT, '22-members-after-invite.png'), fullPage: true });

  // Open the tenant switcher to demonstrate the Alpine dropdown
  await pageA.locator('.c-switcher__trigger').click();
  await pageA.waitForTimeout(200);
  await waitForAlpine(pageA); await pageA.screenshot({ path: path.join(OUT, '23-switcher-open.png'), fullPage: true });
  await pageA.keyboard.press('Escape');

  // ----- Invitee context (fresh browser context = fresh cookies) -----
  const ctxB = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pageB = await ctxB.newPage();
  await signup(pageB, invitee.name, invitee.email, invitee.ws, invitee.pw);
  await verify(pageB, invitee.email);
  await login(pageB, invitee.email, invitee.pw);
  await waitForAlpine(pageB); await pageB.screenshot({ path: path.join(OUT, '24-invitee-own-dashboard.png'), fullPage: true });

  // Click the invitation link from the email
  const inviteURL = await getMailLink(invitee.email, "You're invited", 'invitations');
  await pageB.goto(inviteURL, { waitUntil: 'networkidle' });
  await waitForAlpine(pageB); await pageB.screenshot({ path: path.join(OUT, '25-invitation-page.png'), fullPage: true });

  // Accept it
  await Promise.all([
    pageB.waitForNavigation({ waitUntil: 'networkidle' }),
    pageB.locator('form[action$="/accept"] button[type=submit]').click(),
  ]);
  await waitForAlpine(pageB); await pageB.screenshot({ path: path.join(OUT, '26-invitee-in-owner-workspace.png'), fullPage: true });

  // Open switcher to show the invitee now sees both workspaces
  await pageB.locator('.c-switcher__trigger').click();
  await pageB.waitForTimeout(200);
  await waitForAlpine(pageB); await pageB.screenshot({ path: path.join(OUT, '27-invitee-switcher.png'), fullPage: true });
  await pageB.keyboard.press('Escape');

  // Account page for the invitee
  await pageB.goto(`${BASE}/account`, { waitUntil: 'networkidle' });
  await waitForAlpine(pageB); await pageB.screenshot({ path: path.join(OUT, '28-invitee-account.png'), fullPage: true });

  // Owner refreshes members page — invitee should now show as a member, invitation gone
  await pageA.goto(`${BASE}/t/${slugFromName(owner.ws)}/members`, { waitUntil: 'networkidle' });
  await waitForAlpine(pageA); await pageA.screenshot({ path: path.join(OUT, '29-members-after-accept.png'), fullPage: true });

  console.log(JSON.stringify({ owner: owner.email, invitee: invitee.email }, null, 2));
  await browser.close();
})().catch(err => { console.error(err); process.exit(1); });

function slugFromName(n) {
  return n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}
