// Verify the Password tab exists in Roundcube Settings.
const { chromium } = require('playwright');
(async () => {
  const browser = await chromium.launch({ headless: false });
  const page = await browser.newContext({ viewport: { width: 1280, height: 900 } }).then(c => c.newPage());
  await page.goto('https://mail.shopifygmc.com/');
  await page.fill('input[name=_user]', 'admin@shopifygmc.com');
  await page.fill('input[name=_pass]', 'KBhLevPX2Hev8shPjTDA99Ty');
  await Promise.all([
    page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}),
    page.click('button[type=submit]'),
  ]);
  console.log('post-login URL:', page.url());

  // Visit Settings
  await page.goto('https://mail.shopifygmc.com/?_task=settings', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/tmp/settings.png', fullPage: true });
  console.log('settings page screenshot saved to /tmp/settings.png');

  // Look for the Password section in the sidebar
  const items = await page.locator('#settings-tabs a, .listing a').allTextContents();
  console.log('Settings sidebar items:', items);

  // Probe the explicit URL for the password section
  await page.goto('https://mail.shopifygmc.com/?_task=settings&_action=plugin.password', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/tmp/settings-password.png', fullPage: true });
  const url = page.url();
  const title = await page.title();
  console.log('password page url:', url);
  console.log('password page title:', title);

  await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
