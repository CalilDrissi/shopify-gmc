// Walk the platform admin TOTP flow far enough to land on /admin/gmc as
// the seeded super_admin, so the server emits a log line with
// platform_admin_id populated.

const { chromium } = require('playwright');
const { authenticator } = require('otplib');
const { execSync } = require('child_process');
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

(async () => {
  // Reset TOTP enrolment so we can walk the enroll flow to TOTP-verified.
  execSync(`docker exec gmcauditor-postgres psql -U gmc -d gmcauditor -c "UPDATE platform_admins SET totp_secret=NULL, totp_enrolled_at=NULL WHERE role='super';"`, { stdio: 'ignore' });

  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  await page.goto('http://localhost:8080/admin/login');
  await page.fill('input[name=email]', 'admin@gmcauditor.local');
  await page.fill('input[name=password]', 'super-strong-pass-2026');
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);
  // /admin/totp/enroll
  const secret = await page.locator('input[name=secret]').inputValue();
  await page.fill('input[name=code]', authenticator.generate(secret));
  await Promise.all([page.waitForNavigation(), page.click('button[type=submit]')]);

  // We're on /admin (dashboard). Hit /admin/gmc so the log line captures
  // platform_admin_id.
  await page.goto('http://localhost:8080/admin/gmc');
  await sleep(500);
  await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
