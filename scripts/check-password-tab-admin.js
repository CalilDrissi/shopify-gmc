// Verify Settings → Password is visible for admin@shopifygmc.com (the
// real account the user logs in with). Take a screenshot of the Settings
// page so we can show the user exactly what they should see.
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  await page.goto('https://mail.shopifygmc.com/');
  await page.fill('input[name=_user]', 'admin@shopifygmc.com');
  await page.fill('input[name=_pass]', 'KBhLevPX2Hev8shPjTDA99Ty');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
    page.click('button[type=submit]'),
  ]);
  console.log('post-login URL:', page.url());

  // Click the gear icon (Settings)
  await page.goto('https://mail.shopifygmc.com/?_task=settings', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/workspaces/shopify-gmc/tmp/settings-admin.png', fullPage: true });
  const items = await page.locator('a').filter({ hasText: /^(Preferences|Folders|Identities|Responses|Password|Filters)$/ }).allTextContents();
  console.log('Settings sidebar items visible to admin@shopifygmc.com:', items);

  // Visit the Password page directly
  await page.goto('https://mail.shopifygmc.com/?_task=settings&_action=plugin.password', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/workspaces/shopify-gmc/tmp/settings-admin-password.png', fullPage: true });
  const inputs = await page.$$eval('input', els => els.map(e => ({ name: e.name, type: e.type })));
  console.log('Password page form inputs:', inputs);

  await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
