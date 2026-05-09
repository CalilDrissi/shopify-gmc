#!/usr/bin/env node
// End-to-end verification of Phase 5 (per-mailbox Sieve filter rules).
//
// What this proves:
//   1. `mailbox filter-add EMAIL from <addr> move Junk` writes a sieve rule
//      that's picked up by Dovecot's LMTP delivery.
//   2. A real swaks delivery from the matched address lands in `.Junk/cur/`
//      rather than the inbox.
//   3. `mailbox filter-del EMAIL 1` removes the rule and a fresh delivery
//      from the same address now lands in the inbox.
//
// Backend-only — no UI auth needed. Pattern from check-admin-mail-quota.js.

const { execSync } = require('child_process');

const stamp = Date.now().toString(36);
const email = `ftest-${stamp}@shopifygmc.com`;
const sender = `noreply-${stamp}@example.com`;
const SSH_HOST = process.env.SSH_HOST || 'root@62.169.16.57';
const SSH_KEY  = process.env.SSH_KEY  || `${process.env.HOME}/.ssh/gmcauditor_deploy`;

function ssh(cmd) {
  const sshCmd = `ssh -i ${SSH_KEY} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${SSH_HOST} ${JSON.stringify(cmd)}`;
  return execSync(sshCmd, { encoding: 'utf8', maxBuffer: 5 * 1024 * 1024 });
}

function step(label, fn) {
  process.stdout.write(`\n→ ${label}\n`);
  try {
    fn();
    process.stdout.write(`  ✓ ok\n`);
  } catch (e) {
    process.stdout.write(`  ✗ FAIL: ${e.message}\n`);
    cleanup();
    process.exit(1);
  }
}
function assert(cond, msg) { if (!cond) throw new Error(msg); }

function cleanup() {
  try { ssh(`echo '${email}' | mailbox del ${email} || true`); } catch (_) {}
}

function localPart(e) { return e.split('@')[0]; }
function domain(e) { return e.split('@')[1]; }

function countMail(folder) {
  // Fresh deliveries land in maildir's `new/`; only after IMAP read does
  // the mail get moved into `cur/`. Count both so we don't miss messages.
  const root = folder === 'junk'
    ? `/var/mail/vmail/${domain(email)}/${localPart(email)}/.Junk`
    : `/var/mail/vmail/${domain(email)}/${localPart(email)}`;
  const out = ssh(`(ls -1 ${root}/cur 2>/dev/null; ls -1 ${root}/new 2>/dev/null) | wc -l`).trim();
  return parseInt(out, 10) || 0;
}

(async () => {
  console.log(`Phase 5 verification — mailbox: ${email}`);

  step('1. create test mailbox', () => {
    const out = ssh(`mailbox add ${email}`);
    assert(out.includes('ok:'), 'mailbox add failed: ' + out);
  });

  step('2. add filter: from sender → move to Junk', () => {
    ssh(`mailbox filter-add ${email} from '${sender}' move Junk`);
    const list = ssh(`mailbox filter-list ${email}`);
    assert(list.includes(sender), `rule not in list:\n${list}`);
    assert(list.includes('move'), `expected move action in list:\n${list}`);
  });

  step('3. send mail from filtered sender — must land in Junk', () => {
    const inboxBefore = countMail('inbox');
    const junkBefore = countMail('junk');
    ssh(`swaks --to ${email} --from ${sender} --server localhost:25 ` +
        `--header 'Subject: should-be-junked-${stamp}' --body 'spam' >/dev/null 2>&1`);
    ssh(`sleep 2`);
    const inboxAfter = countMail('inbox');
    const junkAfter = countMail('junk');
    assert(junkAfter === junkBefore + 1, `expected +1 in Junk, got ${junkBefore}→${junkAfter}`);
    assert(inboxAfter === inboxBefore, `inbox should be unchanged, got ${inboxBefore}→${inboxAfter}`);
  });

  step('4. delete filter rule', () => {
    ssh(`mailbox filter-del ${email} 1`);
    const list = ssh(`mailbox filter-list ${email}`);
    assert(!list.includes(sender), `rule still present after delete:\n${list}`);
  });

  step('5. send mail from same sender — now lands in inbox', () => {
    const inboxBefore = countMail('inbox');
    const junkBefore = countMail('junk');
    ssh(`swaks --to ${email} --from ${sender} --server localhost:25 ` +
        `--header 'Subject: should-go-inbox-${stamp}' --body 'normal' >/dev/null 2>&1`);
    ssh(`sleep 2`);
    const inboxAfter = countMail('inbox');
    const junkAfter = countMail('junk');
    assert(inboxAfter === inboxBefore + 1, `expected +1 in inbox, got ${inboxBefore}→${inboxAfter}`);
    assert(junkAfter === junkBefore, `Junk should be unchanged, got ${junkBefore}→${junkAfter}`);
  });

  step('6. cleanup', cleanup);

  console.log('\n✓ Phase 5 verified end-to-end (Sieve filter rules)');
  process.exit(0);
})().catch(e => {
  console.error('\nUNEXPECTED:', e);
  cleanup();
  process.exit(1);
});
