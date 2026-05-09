#!/usr/bin/env node
// End-to-end verification of Phase 6 (per-mailbox spam settings).
//
// What this proves (backend-only — no UI / no Playwright):
//   1. `mailbox add` creates a fresh test mailbox.
//   2. `mailbox spam-block EMAIL spammer@example.com` writes a managed
//      spam.sieve and the active .dovecot.sieve includes it.
//   3. `mailbox spam-list EMAIL` round-trips the blacklist correctly.
//   4. Mail sent from the blacklisted sender lands in `.Junk/cur/` (not the
//      inbox `cur/`) — proves the sieve script ran.
//   5. `mailbox spam-unblock EMAIL spammer@example.com` clears it; a follow-up
//      message from the same sender now lands in the inbox `cur/`.
//   6. Cleanup deletes the mailbox.
//
// rspamd-dependent threshold flow is intentionally NOT exercised here —
// the box doesn't have rspamd installed, so X-Spam-Score is never written.
// The handler renders a banner saying so.
//
// Exits 0 on success, 1 on any assertion failure.

const { execSync } = require('child_process');

const stamp = Date.now().toString(36);
const email = `spam-${stamp}@shopifygmc.com`;
const SPAMMER = 'spammer@example.com';
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

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

function cleanup() {
  try {
    ssh(`echo '${email}' | mailbox del ${email} || true`);
  } catch (_) { /* best-effort */ }
}

function sendFrom(from, subject) {
  // swaks --from is sender (envelope MAIL FROM); receiver-side Sieve
  // `envelope :is "from"` matches that exact string.
  ssh(
    `swaks --to ${email} --from ${from} --server localhost:25 ` +
    `--header 'Subject: ${subject}' --body 'phase6 verify' >/dev/null 2>&1`
  );
}

function countMessagesIn(folder) {
  // folder is the maildir folder NAME ("cur", ".Junk", etc.). Count both
  // `new/` (freshly delivered, not yet IMAP-read) and `cur/` (read).
  const local = email.split('@')[0];
  const domain = email.split('@')[1];
  // If folder ends in /cur, strip it so we can count new+cur of its parent.
  const root = folder.replace(/\/?cur$/, '');
  const base = `/var/mail/vmail/${domain}/${local}${root ? '/' + root : ''}`;
  try {
    const out = ssh(`(ls -1 ${base}/cur 2>/dev/null; ls -1 ${base}/new 2>/dev/null) | wc -l`);
    return parseInt(out.trim(), 10) || 0;
  } catch (_) {
    return 0;
  }
}

(async () => {
  console.log(`Phase 6 verification — mailbox: ${email}`);

  step('1. create mailbox', () => {
    const out = ssh(`mailbox add ${email}`);
    assert(out.includes('ok:') && out.includes(email), 'mailbox add failed: ' + out);
  });

  step('2. blacklist spammer; spam-list reflects it', () => {
    ssh(`mailbox spam-block ${email} ${SPAMMER}`);
    const out = ssh(`mailbox spam-list ${email}`);
    assert(/blacklist:.*spammer@example\.com/.test(out), 'spam-list missing blacklist: ' + out);
    assert(/whitelist:\s*$/m.test(out), 'whitelist should be empty: ' + out);
    assert(/threshold:none/.test(out), 'threshold should default to none: ' + out);
  });

  step('3. spam.sieve exists and active .dovecot.sieve includes it', () => {
    const local = email.split('@')[0];
    const domain = email.split('@')[1];
    const sieveDir = `/var/mail/vmail/${domain}/${local}/sieve`;
    const active = `/var/mail/vmail/${domain}/${local}/.dovecot.sieve`;
    const ls = ssh(`ls ${sieveDir}/spam.sieve && cat ${active}`);
    assert(ls.includes('include :personal "spam"'), 'active .dovecot.sieve does not include spam: ' + ls);
  });

  step('4. mail from blacklisted sender lands in .Junk', () => {
    const beforeInbox = countMessagesIn('cur');
    const beforeJunk  = countMessagesIn('.Junk/cur');
    sendFrom(SPAMMER, `blacklisted-${stamp}`);
    // LMTP delivery is fast but give it a beat.
    ssh(`sleep 3`);
    const afterInbox = countMessagesIn('cur');
    const afterJunk  = countMessagesIn('.Junk/cur');
    assert(afterJunk === beforeJunk + 1, `expected +1 in .Junk/cur, got before=${beforeJunk} after=${afterJunk}`);
    assert(afterInbox === beforeInbox, `expected inbox unchanged, got before=${beforeInbox} after=${afterInbox}`);
  });

  step('5. unblock + new message goes to inbox', () => {
    ssh(`mailbox spam-unblock ${email} ${SPAMMER}`);
    const list = ssh(`mailbox spam-list ${email}`);
    assert(/blacklist:\s*$/m.test(list), 'blacklist should be empty after unblock: ' + list);

    const beforeInbox = countMessagesIn('cur');
    const beforeJunk  = countMessagesIn('.Junk/cur');
    sendFrom(SPAMMER, `unblocked-${stamp}`);
    ssh(`sleep 3`);
    const afterInbox = countMessagesIn('cur');
    const afterJunk  = countMessagesIn('.Junk/cur');
    assert(afterInbox === beforeInbox + 1, `expected +1 in inbox cur/, got before=${beforeInbox} after=${afterInbox}`);
    assert(afterJunk === beforeJunk, `expected .Junk unchanged, got before=${beforeJunk} after=${afterJunk}`);
  });

  step('6. cleanup', () => {
    cleanup();
    let stillThere = '';
    try { stillThere = ssh(`grep '^${email}:' /etc/dovecot/users || true`); } catch (_) {}
    assert(!stillThere.includes(email), 'mailbox still in /etc/dovecot/users after del');
  });

  console.log('\n✓ Phase 6 verified end-to-end (sieve blacklist routes to Junk, unblock routes to inbox)');
  process.exit(0);
})().catch(e => {
  console.error('\nUNEXPECTED ERROR:', e);
  cleanup();
  process.exit(1);
});
