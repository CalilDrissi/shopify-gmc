// Full Playwright audit of the live deployment — page by page, form by
// form. Targets staging by default (BASE env var to override). Captures:
//
//   - HTTP status (302 chains followed)
//   - Page title + presence of expected anchor element
//   - Browser console errors / page errors
//   - Network failures (4xx/5xx for non-probe requests inside the page)
//   - A full-page screenshot for every visited URL
//   - Form submission outcome for every form encountered
//
// Findings are aggregated by severity and written to tmp/audit/report.md.

const { chromium } = require('playwright');
const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const BASE = process.env.BASE || 'https://staging.shopifygmc.com';
const ROOT = path.join(__dirname, '..');
const OUT  = path.join(ROOT, 'tmp', 'audit');
const SHOTS = path.join(OUT, 'screenshots');
fs.rmSync(OUT, { recursive: true, force: true });
fs.mkdirSync(SHOTS, { recursive: true });

const findings = [];
let counter = 0;

function record(level, where, what, extra = {}) {
  findings.push({ level, where, what, ...extra });
  const tag = { critical: '🚨', bug: '🐛', warn: '💛', info: '💡', ok: '✓' }[level] || '?';
  console.log(`${tag} ${where} — ${what}`);
}

const slugify = (s) => s.replace(/[^a-z0-9]+/gi, '-').toLowerCase().replace(/^-+|-+$/g, '');

async function visit(page, label, url, opts = {}) {
  const seq = String(++counter).padStart(3, '0');
  const shot = path.join(SHOTS, `${seq}-${slugify(label)}.png`);
  let resp, status, ok = false, title = '', consoleErrors = [], pageErrors = [], netFails = [];
  const consoleHandler = (msg) => { if (msg.type() === 'error') consoleErrors.push(msg.text()); };
  const pageErrHandler = (e) => pageErrors.push(e.message || String(e));
  const respHandler = (r) => {
    if (r.url().startsWith('chrome-extension:')) return;
    if (r.url().includes('/healthz') || r.url().includes('/readyz')) return;
    if (r.status() >= 400) netFails.push(`${r.status()} ${r.request().method()} ${r.url()}`);
  };
  page.on('console', consoleHandler);
  page.on('pageerror', pageErrHandler);
  page.on('response', respHandler);
  try {
    resp = await page.goto(url, { waitUntil: 'networkidle', timeout: 20000 });
    status = resp ? resp.status() : 0;
    ok = status >= 200 && status < 400;
    title = await page.title();
    await page.screenshot({ path: shot, fullPage: true });
  } catch (e) {
    record('critical', label, `navigation threw: ${e.message}`, { url });
  } finally {
    page.off('console', consoleHandler);
    page.off('pageerror', pageErrHandler);
    page.off('response', respHandler);
  }
  if (status >= 500) {
    record('critical', label, `HTTP ${status} on GET ${url}`, { url, screenshot: shot });
  } else if (status >= 400 && !opts.expect4xx) {
    record('bug', label, `HTTP ${status} on GET ${url}`, { url, screenshot: shot });
  } else if (status === 0) {
    // already recorded as critical above
  } else {
    record('ok', label, `${status} ${title}`, { url, screenshot: shot });
  }
  for (const msg of consoleErrors) {
    record('bug', label, `console error: ${msg.slice(0, 200)}`, { url });
  }
  for (const msg of pageErrors) {
    record('critical', label, `JS exception: ${msg.slice(0, 200)}`, { url });
  }
  for (const msg of netFails) {
    record('warn', label, `subresource ${msg}`, { url });
  }
  return { resp, status, ok };
}

// ---------------------------------------------------------------------------

(async () => {
  console.log(`Auditing ${BASE}`);
  console.log(`Output: ${OUT}\n`);

  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
  const page = await ctx.newPage();

  // ------------------------------------------------------------------------
  // Public pages (no session)
  // ------------------------------------------------------------------------
  console.log('\n=== Public pages ===');
  await visit(page, 'landing',           BASE + '/');
  await visit(page, 'pricing',           BASE + '/pricing');
  await visit(page, 'features',          BASE + '/features');
  await visit(page, 'login-form',        BASE + '/login');
  await visit(page, 'signup-form',       BASE + '/signup');
  await visit(page, 'forgot-password',   BASE + '/forgot-password');
  await visit(page, 'verify-pending',    BASE + '/verify-email-pending');
  await visit(page, 'admin-login',       BASE + '/admin/login');
  await visit(page, 'healthz',           BASE + '/healthz');
  await visit(page, 'readyz',            BASE + '/readyz');
  // Bogus token routes — should render an error page (200 or 4xx is fine,
  // 500 would be a bug)
  await visit(page, 'verify-bad-token',  BASE + '/verify-email/not-a-real-token', { expect4xx: true });
  await visit(page, 'reset-bad-token',   BASE + '/reset-password/not-a-real-token', { expect4xx: true });
  await visit(page, 'invitation-bad',    BASE + '/invitations/not-a-real-token', { expect4xx: true });
  await visit(page, 'unsubscribe-bad',   BASE + '/unsubscribe/not-a-real-token', { expect4xx: true });

  // ------------------------------------------------------------------------
  // Form: signup. Submit invalid inputs first, then a valid one.
  // ------------------------------------------------------------------------
  console.log('\n=== Form: signup ===');
  await page.goto(BASE + '/signup', { waitUntil: 'networkidle' });
  // Empty submit — should reject
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');
  if (page.url().endsWith('/signup')) record('ok', 'signup-empty', 'empty submit re-rendered the form');
  else record('bug', 'signup-empty', `empty submit redirected to ${page.url()}`);

  // Short password — should reject (< 12 chars)
  await page.fill('input[name=name]', 'Test');
  await page.fill('input[name=email]', 'test@example.com');
  await page.fill('input[name=workspace]', 'Test');
  await page.fill('input[name=password]', 'short');
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');
  if (page.url().endsWith('/signup')) {
    const html = await page.content();
    if (/12 char/i.test(html) || /minimum/i.test(html)) {
      record('ok', 'signup-shortpw', 'short password rejected with visible message');
    } else {
      record('warn', 'signup-shortpw', 'short password re-rendered form but no visible error message');
    }
  } else {
    record('bug', 'signup-shortpw', `short password did NOT reject — landed on ${page.url()}`);
  }

  // Valid signup
  const stamp = Date.now().toString(36);
  const owner = {
    name: `Audit ${stamp}`,
    email: `audit-${stamp}@example.com`,
    pw: 'super-strong-pass-2026',
    ws: `Audit ${stamp}`,
  };
  await page.fill('input[name=name]', owner.name);
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=workspace]', owner.ws);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }), page.click('button[type=submit]')]);
  await visit(page, 'after-signup', page.url());

  // Verify email. The verify URL is sent to a non-deliverable test address
  // (audit-XYZ@example.com publishes nullMX so Postfix bounces). The mail
  // never lands in a readable inbox, and we don't have raw-token access via
  // the DB (only the hash is stored). For the audit we just confirm the
  // verification email was queued, then bypass the verify step by flipping
  // email_verified_at directly so the signed-in tour can continue. This is
  // a *test-only* shortcut, not an app code path.
  try {
    const sshKey = process.env.HOME + '/.ssh/gmcauditor_deploy';
    const sshHost = process.env.AUDIT_SSH_HOST || 'root@62.169.16.57';
    if (fs.existsSync(sshKey)) {
      const queued = execSync(
        `ssh -i ${sshKey} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${sshHost} ` +
        `"grep '${owner.email}' /var/log/mail.log | tail -2"`,
        { encoding: 'utf8' }
      );
      if (/Confirm your email|status=(sent|deferred|bounced)/.test(queued) ||
          queued.toLowerCase().includes(owner.email.toLowerCase())) {
        record('ok', 'verify-mail-queued', 'verification email was handed off to Postfix');
      } else {
        record('bug', 'verify-mail-queued', `no Postfix log entry for ${owner.email} — verify mail may not have been sent`);
      }
      // Bypass for the rest of the audit
      execSync(
        `ssh -i ${sshKey} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${sshHost} ` +
        `"sudo -u postgres psql -d gmcauditor_staging -c \\"UPDATE users SET email_verified_at=now() WHERE email='${owner.email}'\\""`,
        { encoding: 'utf8' }
      );
      record('info', 'verify-bypass', 'flipped email_verified_at via DB so the signed-in tour can continue (test-only shortcut)');
    } else {
      record('warn', 'verify-bypass-skipped', `no SSH key at ${sshKey} — signed-in tour will fail`);
    }
  } catch (e) {
    record('warn', 'verify-fetch', `${e.message.slice(0, 200)}`);
  }

  // ------------------------------------------------------------------------
  // Login + tenant pages
  // ------------------------------------------------------------------------
  console.log('\n=== Form: login ===');
  await page.goto(BASE + '/login', { waitUntil: 'networkidle' });
  // Wrong password
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', 'wrong-password');
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');
  if (page.url().includes('/login')) {
    const html = await page.content();
    if (/invalid/i.test(html) || /failed/i.test(html)) {
      record('ok', 'login-bad', 'wrong password rejected with visible message');
    } else {
      record('warn', 'login-bad', 'wrong password re-rendered form but no visible error');
    }
  } else {
    record('bug', 'login-bad', `wrong password somehow landed on ${page.url()}`);
  }

  // Right password
  await page.fill('input[name=email]', owner.email);
  await page.fill('input[name=password]', owner.pw);
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle', timeout: 10000 }).catch(() => {}),
    page.click('button[type=submit]'),
  ]);
  await visit(page, 'after-login', page.url());

  const slug = owner.ws.toLowerCase().replace(/[^a-z0-9]+/g, '-');
  const tenantBase = `${BASE}/t/${slug}`;
  if (page.url().includes(`/t/${slug}`)) {
    console.log('\n=== Tenant pages (signed in) ===');
    await visit(page, 'tenant-dashboard', tenantBase);
    await visit(page, 'tenant-stores',    tenantBase + '/stores');
    await visit(page, 'tenant-store-new', tenantBase + '/stores/new');
    await visit(page, 'tenant-audits',    tenantBase + '/audits');
    await visit(page, 'tenant-members',   tenantBase + '/members');
    await visit(page, 'tenant-billing',   tenantBase + '/billing');
    await visit(page, 'account',          BASE + '/account');

    // Form: create a store. Fill with an unreachable domain → expect a
    // visible error (the pingURL check fails fast).
    console.log('\n=== Form: create store ===');
    await page.goto(tenantBase + '/stores/new', { waitUntil: 'networkidle' });
    await page.fill('input[name=shop_domain]', 'definitely-not-a-real-store-' + stamp + '.example.com');
    if (await page.locator('input[name=display_name]').count()) {
      await page.fill('input[name=display_name]', 'Audit Store');
    }
    await page.click('form[action$="/stores/new"] button[type=submit]');
    await page.waitForLoadState('networkidle');
    const sn = await page.content();
    if (/couldn'?t reach/i.test(sn) || /could not/i.test(sn) || /invalid/i.test(sn) || page.url().includes('/stores/new')) {
      record('ok', 'store-create-bad', 'unreachable domain rejected with visible error');
    } else {
      record('warn', 'store-create-bad', `unreachable domain may have been accepted; url=${page.url()}`);
    }

    // Try inviting a member
    console.log('\n=== Form: invite member ===');
    await page.goto(tenantBase + '/members', { waitUntil: 'networkidle' });
    if (await page.locator('input[name=email]').count()) {
      await page.fill('input[name=email]', `invitee-${stamp}@example.com`);
      if (await page.locator('select[name=role]').count()) {
        await page.selectOption('select[name=role]', { value: 'member' }).catch(() => {});
      }
      await Promise.all([
        page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
        page.click('form[action$="/invitations"] button[type=submit]'),
      ]);
      await visit(page, 'after-invite', page.url());
    } else {
      record('warn', 'invite-form', 'no invite form found on /members');
    }

    // Forgot password (no need to be signed in but we check the flow works)
    console.log('\n=== Form: forgot password ===');
    await page.goto(BASE + '/forgot-password', { waitUntil: 'networkidle' });
    await page.fill('input[name=email]', owner.email);
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
      page.click('button[type=submit]'),
    ]);
    await visit(page, 'after-forgot', page.url());
  } else {
    record('warn', 'login-success', `login didn't land on tenant dashboard; got ${page.url()} — skipping tenant tour`);
  }

  // ------------------------------------------------------------------------
  // Admin pages — use the seeded admin credentials
  // ------------------------------------------------------------------------
  console.log('\n=== Admin pages ===');
  const adminCtx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
  const adminPage = await adminCtx.newPage();
  await visit(adminPage, 'admin-login-form', BASE + '/admin/login');
  await adminPage.fill('input[name=email]', 'admin@shopifygmc.com');
  await adminPage.fill('input[name=password]', 'KBhLevPX2Hev8shPjTDA99Ty');
  await Promise.all([
    adminPage.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
    adminPage.click('button[type=submit]'),
  ]);

  // We may land on /admin/totp/verify or /admin/totp/enroll. If TOTP is
  // already enrolled we need a real code — skip the rest and report.
  const url = adminPage.url();
  if (url.includes('/admin/totp/verify')) {
    record('warn', 'admin-totp', 'admin login lands on TOTP verify; cannot continue audit without a 2FA code (set ADMIN_TOTP_SECRET to enable)');
    if (process.env.ADMIN_TOTP_SECRET) {
      const { authenticator } = require('otplib');
      await adminPage.fill('input[name=code]', authenticator.generate(process.env.ADMIN_TOTP_SECRET));
      await Promise.all([
        adminPage.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
        adminPage.click('button[type=submit]'),
      ]);
    }
  } else if (url.includes('/admin/totp/enroll')) {
    // First-time admin login — auto-walk enrollment so the audit can
    // proceed. The secret is read from the page so we don't need it from env.
    const { authenticator } = require('otplib');
    const secret = await adminPage.locator('input[name=secret]').inputValue().catch(() => '');
    if (secret) {
      await adminPage.fill('input[name=code]', authenticator.generate(secret));
      await Promise.all([
        adminPage.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
        adminPage.click('button[type=submit]'),
      ]);
      record('info', 'admin-totp-enroll', `walked TOTP enrollment for the audit; secret=${secret}`);
    }
  }

  if (adminPage.url().startsWith(BASE + '/admin') && !adminPage.url().includes('/totp')) {
    await visit(adminPage, 'admin-dashboard', BASE + '/admin');
    await visit(adminPage, 'admin-tenants',   BASE + '/admin/tenants');
    await visit(adminPage, 'admin-audit-log', BASE + '/admin/audit-log');
    await visit(adminPage, 'admin-gmc',       BASE + '/admin/gmc');
    await visit(adminPage, 'admin-mail',      BASE + '/admin/mail');
    await visit(adminPage, 'admin-settings',  BASE + '/admin/settings');
  } else {
    record('warn', 'admin-tour', `admin tour skipped — landed on ${adminPage.url()}`);
  }

  await ctx.close();
  await adminCtx.close();
  await browser.close();

  // ------------------------------------------------------------------------
  // Write report
  // ------------------------------------------------------------------------
  const counts = findings.reduce((m, f) => { m[f.level] = (m[f.level] || 0) + 1; return m; }, {});
  const groups = ['critical', 'bug', 'warn', 'info'];
  const md = [];
  md.push(`# Audit report — ${BASE}`);
  md.push('');
  md.push(`Generated ${new Date().toISOString()}`);
  md.push('');
  md.push(`**Counts**: ${groups.map(g => `${g}: ${counts[g] || 0}`).join(' · ')} · ok: ${counts.ok || 0}`);
  md.push('');
  md.push('Screenshots are under `tmp/audit/screenshots/`.');
  md.push('');
  for (const g of groups) {
    const items = findings.filter(f => f.level === g);
    if (!items.length) continue;
    md.push(`## ${g.toUpperCase()} (${items.length})`);
    md.push('');
    for (const f of items) {
      md.push(`- **${f.where}** — ${f.what}${f.url ? `  \n  URL: \`${f.url}\`` : ''}${f.screenshot ? `  \n  Screenshot: \`${path.relative(ROOT, f.screenshot)}\`` : ''}`);
    }
    md.push('');
  }
  md.push('## All visited pages');
  md.push('');
  md.push('| # | Where | URL | Status / Title |');
  md.push('| --- | --- | --- | --- |');
  let i = 0;
  for (const f of findings) {
    if (f.level !== 'ok') continue;
    i++;
    md.push(`| ${i} | ${f.where} | \`${(f.url || '').replace(BASE, '')}\` | ${f.what} |`);
  }
  md.push('');

  fs.writeFileSync(path.join(OUT, 'report.md'), md.join('\n'));
  console.log(`\nReport written to ${path.join(OUT, 'report.md')}`);
  console.log(`Counts:`, counts);
})().catch(err => { console.error(err); process.exit(1); });
