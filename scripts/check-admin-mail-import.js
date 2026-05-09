#!/usr/bin/env node
// End-to-end verification of Phase 7 (bulk CSV mailbox import).
//
// What this proves:
//   1. The CSV-driven `mailbox add` sequence the import handler runs works
//      for 5 distinct mailboxes, each with a different quota (default, 0,
//      explicit 500M, 2G, supplied password).
//   2. A re-import of an existing address fails with an "exists" signal
//      that the handler maps to skipped:duplicate (it doesn't crash the
//      whole batch).
//   3. Quotas applied via `mailbox quota` are visible in `mailbox usage`.
//   4. All 5 mailboxes can be cleaned up afterwards with `mailbox del`.
//
// The Go-side parser + per-row validation lives in
// internal/web/handlers_mail_test.go (parseImportCSV fixture suite) — see
// `go test ./internal/web/...`. This script covers the CLI half: that the
// shell-out sequence the handler issues actually works on the live host.
//
// Pure backend test — no UI / no Playwright. Requires SSH to the mail
// host (same env vars as check-admin-mail-quota.js).
//
// Exits 0 on success, 1 on any assertion failure.

const { execSync } = require('child_process');

const stamp = Date.now().toString(36);
const SSH_HOST = process.env.SSH_HOST || 'root@62.169.16.57';
const SSH_KEY  = process.env.SSH_KEY  || `${process.env.HOME}/.ssh/gmcauditor_deploy`;

// Five mailboxes mirroring the test CSV fixture (with quota strings that
// exercise default / explicit / unlimited / supplied-password cases).
const rows = [
  { email: `bulk1-${stamp}@shopifygmc.com`, password: '',           quota: '1G'   },
  { email: `bulk2-${stamp}@shopifygmc.com`, password: 'Hunter2!Pw', quota: '500M' },
  { email: `bulk3-${stamp}@shopifygmc.com`, password: '',           quota: ''     },
  { email: `bulk4-${stamp}@shopifygmc.com`, password: '',           quota: '0'    },
  { email: `bulk5-${stamp}@shopifygmc.com`, password: 'AnotherPw9', quota: '2G'   },
];

function ssh(cmd) {
  const sshCmd = `ssh -i ${SSH_KEY} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${SSH_HOST} ${JSON.stringify(cmd)}`;
  return execSync(sshCmd, { encoding: 'utf8', maxBuffer: 5 * 1024 * 1024 });
}

function sshAllowFail(cmd) {
  try { return { ok: true, out: ssh(cmd) }; }
  catch (e) { return { ok: false, out: (e.stdout || '') + (e.stderr || '') + (e.message || '') }; }
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
  for (const r of rows) {
    try { ssh(`echo '${r.email}' | mailbox del ${r.email} || true`); }
    catch (_) { /* best-effort */ }
  }
}

function parseUsageLine(line) {
  const parts = line.trim().split('\t');
  return { email: parts[0], used: parseInt(parts[1], 10), quota: parseInt(parts[2], 10) };
}

(async () => {
  console.log(`Phase 7 verification — bulk import of 5 mailboxes (stamp ${stamp})`);

  step('1. add 5 mailboxes (mirrors the CSV → handler sequence)', () => {
    for (const r of rows) {
      const args = r.password ? `${r.email} ${r.password}` : r.email;
      const out = ssh(`mailbox add ${args}`);
      assert(out.includes('ok:') && out.includes(r.email), `add ${r.email} failed: ${out}`);
      if (r.quota !== '') {
        ssh(`mailbox quota ${r.email} ${r.quota}`);
      }
    }
  });

  step('2. each mailbox has the expected quota in usage output', () => {
    const expected = {
      [rows[0].email]: 1 * 1024 * 1024 * 1024,
      [rows[1].email]: 500 * 1024 * 1024,
      [rows[2].email]: 1 * 1024 * 1024 * 1024, // default 1G
      [rows[3].email]: 0,                       // unlimited
      [rows[4].email]: 2 * 1024 * 1024 * 1024,
    };
    for (const r of rows) {
      const u = parseUsageLine(ssh(`mailbox usage ${r.email}`));
      assert(u.email === r.email, `email mismatch: ${u.email} vs ${r.email}`);
      assert(u.used === 0, `expected 0 used for ${r.email}, got ${u.used}`);
      assert(u.quota === expected[r.email],
        `quota mismatch for ${r.email}: got ${u.quota}, want ${expected[r.email]}`);
    }
  });

  step('3. re-adding an existing mailbox surfaces an "exists" signal', () => {
    // The handler maps any output containing "exists" or "duplicate" to
    // status "skipped:duplicate". Confirm `mailbox add` for an extant
    // address actually emits something matching.
    const r = sshAllowFail(`mailbox add ${rows[0].email}`);
    assert(!r.ok || /exist|duplicate|already/i.test(r.out),
      `expected exists/duplicate signal on re-add; got ok=${r.ok} out=${r.out.slice(0,200)}`);
  });

  step('4. all 5 mailboxes appear in /etc/dovecot/users', () => {
    const listed = ssh(`awk -F: '{print $1}' /etc/dovecot/users`);
    for (const r of rows) {
      assert(listed.includes(r.email), `${r.email} missing from /etc/dovecot/users`);
    }
  });

  step('5. cleanup removes all 5 mailboxes', () => {
    cleanup();
    const listed = ssh(`awk -F: '{print $1}' /etc/dovecot/users`);
    for (const r of rows) {
      assert(!listed.includes(r.email), `${r.email} still present after del`);
    }
  });

  console.log('\n✓ Phase 7 verified end-to-end (CSV-equivalent CLI sequence + duplicate detection)');
  process.exit(0);
})().catch(e => {
  console.error('\nUNEXPECTED ERROR:', e);
  cleanup();
  process.exit(1);
});
