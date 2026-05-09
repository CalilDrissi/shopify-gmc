#!/usr/bin/env node
// End-to-end verification of Phase 3 (per-mailbox vacation auto-responder).
//
// What this proves:
//   1. `mailbox vacation EMAIL set "Subj" "Body"` writes a sieve script.
//   2. `mailbox vacation EMAIL enable` activates it (.dovecot.sieve symlink).
//   3. Mail sent to A from B triggers an auto-reply that lands in B's maildir
//      with the configured subject — i.e. Sieve + managesieved actually fire.
//   4. `mailbox vacation EMAIL disable` removes the active symlink and a
//      subsequent send produces NO new auto-reply at B.
//
// Pure backend test — no UI / no Playwright. Mirrors check-admin-mail-quota.js.
// Exits 0 on success, 1 on any assertion failure.
//
// Env:
//   SSH_HOST  default root@62.169.16.57
//   SSH_KEY   default $HOME/.ssh/gmcauditor_deploy

const { execSync } = require('child_process');

const stamp = Date.now().toString(36);
const recipient = `vac-r-${stamp}@shopifygmc.com`;
const sender    = `vac-s-${stamp}@shopifygmc.com`;
const subject   = `Out of Office ${stamp}`;
const body      = 'Will reply Monday';
const probeSubj = `probe-${stamp}`;

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
  for (const e of [recipient, sender]) {
    try { ssh(`echo '${e}' | mailbox del ${e} || true`); } catch (_) { /* best effort */ }
  }
}

// Count messages in B's maildir whose Subject header contains `needle`.
// Walks /var/mail/vmail/<domain>/<local>/{cur,new} and greps headers.
function countMatching(email, needle) {
  const localPart = email.split('@')[0];
  const domain    = email.split('@')[1];
  const dir       = `/var/mail/vmail/${domain}/${localPart}`;
  const cmd       = `grep -lir ${JSON.stringify('Subject:.*' + needle)} ${dir}/cur ${dir}/new 2>/dev/null | wc -l`;
  const out       = ssh(cmd).trim();
  return parseInt(out, 10) || 0;
}

(async () => {
  console.log(`Phase 3 verification — recipient=${recipient} sender=${sender}`);

  step('1. create both mailboxes', () => {
    ssh(`mailbox add ${recipient}`);
    ssh(`mailbox add ${sender}`);
  });

  step('2. set vacation script on recipient', () => {
    // Pass subject + body via single-quoted shell args; the CLI's escaper
    // handles internal quotes/backslashes when they appear.
    const out = ssh(`mailbox vacation ${recipient} set ${JSON.stringify(subject)} ${JSON.stringify(body)}`);
    assert(out.includes('ok:'), 'vacation set did not report ok: ' + out);
    const status1 = ssh(`mailbox vacation-status ${recipient}`).trim();
    assert(status1 === 'disabled', `expected status=disabled before enable, got ${status1}`);
  });

  step('3. enable vacation', () => {
    const out = ssh(`mailbox vacation ${recipient} enable`);
    assert(out.includes('ok:'), 'enable did not report ok: ' + out);
    const status2 = ssh(`mailbox vacation-status ${recipient}`).trim();
    assert(status2 === 'enabled', `expected status=enabled, got ${status2}`);
  });

  step('4. send mail from B → A; auto-reply lands at B', () => {
    ssh(`swaks --to ${recipient} --from ${sender} --server localhost:25 ` +
        `--header 'Subject: ${probeSubj}' --body 'hello there' >/dev/null 2>&1`);
    // Sieve vacation runs at LMTP delivery time but the reply path goes
    // back through Postfix → LMTP. Give it up to ~10s.
    let replies = 0;
    for (let i = 0; i < 10; i++) {
      ssh(`sleep 1`);
      replies = countMatching(sender, subject);
      if (replies > 0) break;
    }
    assert(replies > 0, `no auto-reply with subject "${subject}" landed at ${sender}`);
  });

  step('5. disable vacation; second send produces no NEW auto-reply', () => {
    const before = countMatching(sender, subject);
    ssh(`mailbox vacation ${recipient} disable`);
    const status3 = ssh(`mailbox vacation-status ${recipient}`).trim();
    assert(status3 === 'disabled', `expected status=disabled after disable, got ${status3}`);

    // Use a fresh probe subject so a slow reply from step 4 can't fool us.
    const probe2 = `probe2-${stamp}`;
    ssh(`swaks --to ${recipient} --from ${sender} --server localhost:25 ` +
        `--header 'Subject: ${probe2}' --body 'second send' >/dev/null 2>&1`);
    ssh(`sleep 5`);
    const after = countMatching(sender, subject);
    assert(after === before, `expected no new auto-replies (before=${before}, after=${after})`);
  });

  step('6. cleanup', () => {
    cleanup();
    let stillThere = '';
    try { stillThere = ssh(`grep -E '^(${recipient}|${sender}):' /etc/dovecot/users || true`); } catch (_) {}
    assert(!stillThere.includes(recipient) && !stillThere.includes(sender), 'mailboxes still present after cleanup');
  });

  console.log('\n✓ Phase 3 verified end-to-end (sieve vacation auto-reply on/off)');
  process.exit(0);
})().catch(e => {
  console.error('\nUNEXPECTED ERROR:', e);
  cleanup();
  process.exit(1);
});
