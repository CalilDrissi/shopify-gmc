const { chromium } = require('playwright');
const stamp = Date.now().toString(36);
const { execSync } = require('child_process');
const ssh = (cmd) => execSync(`ssh -i ~/.ssh/gmcauditor_deploy -o IdentitiesOnly=yes root@62.169.16.57 ${JSON.stringify(cmd)}`, { encoding: 'utf8' });
const email = `pwdbg-${stamp}@shopifygmc.com`;
const pw = 'first-password-' + stamp;
ssh(`mailbox add ${email} '${pw}'`);
(async () => {
  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 1100 } });
  const page = await ctx.newPage();
  await page.goto('https://mail.shopifygmc.com/');
  await page.fill('input[name=_user]', email);
  await page.fill('input[name=_pass]', pw);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}), page.click('button[type=submit]')]);
  await page.goto('https://mail.shopifygmc.com/?_task=settings&_action=plugin.password', { waitUntil: 'networkidle' });
  await page.screenshot({ path: '/tmp/pw-debug.png', fullPage: true });
  // Dump every input on the page
  const inputs = await page.$$eval('input', els => els.map(e => ({ name: e.name, type: e.type })));
  console.log('inputs on /plugin.password page:', JSON.stringify(inputs, null, 2));
  // And any visible error text
  const text = await page.locator('body').textContent();
  console.log('first 800 chars of body:', text.slice(0, 800));
  await browser.close();
  ssh(`echo '${email}' | mailbox del ${email} || true`);
})();
