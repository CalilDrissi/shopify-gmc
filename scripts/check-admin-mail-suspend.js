#!/usr/bin/env node
// End-to-end verification of Phase 2 (suspend / unsuspend mailbox).
//
// What this proves:
//   1. `mailbox add` creates a fresh mailbox; `doveadm auth login` succeeds.
//   2. `mailbox suspend EMAIL` flips the user's 8th-field extras to include
//      `nopassword=y`; the same `doveadm auth login` is now rejected.
//   3. SMTP delivery to the suspended mailbox still lands in the Maildir
//      (cur/ or new/ count grows) — auth gate ≠ delivery gate.
//   4. `mailbox unsuspend EMAIL` strips the token; auth succeeds again.
//   5. Pre-existing per-mailbox quota override survives the suspend/unsuspend
//      round-trip (preserves the userdb_quota_rule that was on the line).
//
// Pure backend test — no UI / no Playwright. Runs against the live mail box
// over SSH; same SSH_HOST / SSH_KEY env vars as check-admin-mail-quota.js.
//
// Exits 0 on success, 1 on any assertion failure.

const { execSync } = require('child_process');

const stamp = Date.now().toString(36);
const email = `stest-${stamp}@shopifygmc.com`;
const pw = 'suspend-probe-' + stamp;
const SSH_HOST = process.env.SSH_HOST || 'root@62.169.16.57';
const SSH_KEY = process.env.SSH_KEY || `${process.env.HOME}/.ssh/gmcauditor_deploy`;

function ssh(cmd, opts = {}) {
  const sshCmd = `ssh -i ${SSH_KEY} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${SSH_HOST} ${JSON.stringify(cmd)}`;
  return execSync(sshCmd, { encoding: 'utf8', maxBuffer: 5 * 1024 * 1024, ...opts });
}

// doveadm auth login exits 0 + prints "passdb: ... auth succeeded" on ok,
// non-zero + "passdb: ... auth failed" on reject. Capture both via 2>&1.
function dovecotAuth(addr, password) {
  try {
    const out = ssh(`doveadm auth login ${addr} ${JSON.stringify(password)} 2>&1; echo EXIT=$?`);
    return { out, ok: /auth succeeded/i.test(out) && /EXIT=0/.test(out) };
  } catch (e) {
    return { out: (e.stdout || '') + (e.stderr || ''), ok: false };
  }
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

function maildirCount() {
  // Count messages across cur/ and new/ for the test mailbox.
  const local = email.split('@')[0];
  const domain = email.split('@')[1];
  try {
    const out = ssh(
      `find /var/mail/vmail/${domain}/${local}/cur /var/mail/vmail/${domain}/${local}/new ` +
      `-type f 2>/dev/null | wc -l`
    );
    return parseInt(out.trim(), 10) || 0;
  } catch (_) {
    return 0;
  }
}

function userLine() {
  return ssh(`grep '^${email}:' /etc/dovecot/users || true`).trim();
}

(async () => {
  console.log(`Phase 2 verification — mailbox: ${email}`);

  step('1. create mailbox + auth succeeds', () => {
    const out = ssh(`mailbox add ${email} ${JSON.stringify(pw)}`);
    assert(out.includes('ok:') && out.includes(email), 'mailbox add failed: ' + out);
    const a = dovecotAuth(email, pw);
    assert(a.ok, `expected auth success after add, got:\n${a.out}`);
  });

  step('2. set a 250M quota override (to verify preservation later)', () => {
    ssh(`mailbox quota ${email} 250M`);
    const line = userLine();
    assert(/userdb_quota_rule=\*:storage=250M/.test(line), 'quota field missing after quota set: ' + line);
  });

  step('3. suspend → auth rejected, quota field preserved', () => {
    ssh(`mailbox suspend ${email}`);
    const line = userLine();
    assert(/nopassword=y/.test(line), 'nopassword=y not present after suspend: ' + line);
    assert(/userdb_quota_rule=\*:storage=250M/.test(line),
      'quota override DROPPED by suspend: ' + line);
    const a = dovecotAuth(email, pw);
    assert(!a.ok, `expected auth FAIL after suspend, got success:\n${a.out}`);
  });

  step('4. SMTP delivery still works while suspended', () => {
    const before = maildirCount();
    // Deliver a small probe via swaks. We tolerate non-zero exit (some
    // post-DATA banners count as failures); the assertion is on the maildir.
    try {
      ssh(`swaks --to ${email} --from postmaster@shopifygmc.com --server localhost:25 ` +
          `--header 'Subject: suspended-probe-${stamp}' --body 'still flowing' >/dev/null 2>&1 || true`);
    } catch (_) { /* swallow — verified via maildir below */ }
    // LMTP delivery is fast but not synchronous; give it a moment.
    ssh(`sleep 2`);
    const after = maildirCount();
    assert(after > before,
      `expected maildir count to grow during suspension, before=${before} after=${after}`);
  });

  step('5. unsuspend → auth succeeds, quota field still preserved', () => {
    ssh(`mailbox unsuspend ${email}`);
    const line = userLine();
    assert(!/nopassword=y/.test(line), 'nopassword=y still present after unsuspend: ' + line);
    assert(/userdb_quota_rule=\*:storage=250M/.test(line),
      'quota override DROPPED by unsuspend: ' + line);
    const a = dovecotAuth(email, pw);
    assert(a.ok, `expected auth success after unsuspend, got:\n${a.out}`);
  });

  step('6. cleanup', () => {
    cleanup();
    const stillThere = ssh(`grep '^${email}:' /etc/dovecot/users || true`);
    assert(!stillThere.includes(email), 'mailbox still in /etc/dovecot/users after del');
  });

  console.log('\n✓ Phase 2 verified end-to-end (CLI + Dovecot passdb suspension)');
  process.exit(0);
})().catch(e => {
  console.error('\nUNEXPECTED ERROR:', e);
  cleanup();
  process.exit(1);
});
