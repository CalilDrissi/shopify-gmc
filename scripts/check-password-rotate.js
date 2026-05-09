// End-to-end test of the Roundcube password change.
// 1. Create a throwaway mailbox via the mailbox CLI
// 2. Log into Roundcube as that mailbox
// 3. Submit Settings → Password with old + new
// 4. Log out, log back in with the new password — should succeed
// 5. Old password should NOT log in
const { chromium } = require('playwright');
const { execSync } = require('child_process');
const stamp = Date.now().toString(36);
const email = `pwtest-${stamp}@shopifygmc.com`;
const pw1 = 'first-password-' + stamp;
const pw2 = 'second-password-' + stamp;
const ssh = (cmd) =>
  execSync(`ssh -i ~/.ssh/gmcauditor_deploy -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no root@62.169.16.57 ${JSON.stringify(cmd)}`,
    { encoding: 'utf8' });

(async () => {
  console.log('1. create mailbox', email);
  ssh(`mailbox add ${email} '${pw1}'`);

  const browser = await chromium.launch({ headless: false });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();

  console.log('2. log into Roundcube with first password');
  await page.goto('https://mail.shopifygmc.com/');
  await page.fill('input[name=_user]', email);
  await page.fill('input[name=_pass]', pw1);
  await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}), page.click('button[type=submit]')]);
  if (!page.url().includes('_task=mail')) throw new Error('first login failed: ' + page.url());
  console.log('   OK, landed on inbox');

  console.log('3. open Settings → Password');
  await page.goto('https://mail.shopifygmc.com/?_task=settings&_action=plugin.password', { waitUntil: 'networkidle' });
  // Form fields: _curpasswd, _newpasswd, _confpasswd
  // Roundcube plugin uses the session's IMAP password as the "current" —
  // form only asks for the new password twice.
  await page.fill('input[name=_newpasswd]', pw2);
  await page.fill('input[name=_confpasswd]', pw2);
  await page.click('button.btn-primary, input[type=submit], button[type=submit]');
  await page.waitForTimeout(2000);
  await page.screenshot({ path: '/tmp/passwd-result.png', fullPage: true });
  const visible = await page.locator('.boxconfirmation, .alert-success').textContent().catch(() => '');
  console.log('   confirmation banner:', visible || '(none — see screenshot /tmp/passwd-result.png)');

  console.log('4. log out');
  // Just close + new context
  await ctx.close();

  const ctx2 = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const p2 = await ctx2.newPage();

  console.log('5. log in with NEW password');
  await p2.goto('https://mail.shopifygmc.com/');
  await p2.fill('input[name=_user]', email);
  await p2.fill('input[name=_pass]', pw2);
  await Promise.all([p2.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}), p2.click('button[type=submit]')]);
  const newOk = p2.url().includes('_task=mail');
  console.log('   new password login:', newOk ? '✓ SUCCESS' : '✗ FAILED ' + p2.url());

  console.log('6. log in with OLD password (should fail)');
  await ctx2.close();
  const ctx3 = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const p3 = await ctx3.newPage();
  await p3.goto('https://mail.shopifygmc.com/');
  await p3.fill('input[name=_user]', email);
  await p3.fill('input[name=_pass]', pw1);
  await Promise.all([p3.waitForNavigation({ waitUntil: 'networkidle' }).catch(() => {}), p3.click('button[type=submit]')]);
  const oldRejected = !p3.url().includes('_task=mail');
  console.log('   old password rejected:', oldRejected ? '✓ YES (correct)' : '✗ STILL ACCEPTED (BAD)');

  console.log('\n7. cleanup — delete the throwaway mailbox');
  ssh(`echo '${email}' | mailbox del ${email} || true`);

  await browser.close();
  if (newOk && oldRejected) {
    console.log('\n✓ password rotation works end-to-end');
    process.exit(0);
  } else {
    console.log('\n✗ password rotation FAILED');
    process.exit(1);
  }
})().catch(e => { console.error(e); process.exit(1); });
