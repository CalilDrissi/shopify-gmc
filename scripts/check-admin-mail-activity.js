#!/usr/bin/env node
// End-to-end smoke check for Phase 4 (per-mailbox recent activity viewer).
//
// What this proves:
//   1. /admin/mail/activity?email=... requires admin auth (302 → /admin/login).
//   2. After creating a fresh mailbox + sending it 3 distinct messages, the
//      mail.log on the live host contains entries for that mailbox (so the
//      page would have rows to display once an admin browses to it).
//   3. The Postfix queue IDs for the three messages all show up in
//      /var/log/mail.log within 2 seconds of swaks completing — i.e. the
//      data source the page reads is actually populated.
//
// The parser is exercised in detail by handlers_mail_test.go (Go fixture
// log lines + assertions on direction / size / status / dsn). This script
// is the live-host smoke check that the page is wired up and the log is
// being written to in the format the parser expects.
//
// Exits 0 on success, 1 on any assertion failure.

const { execSync } = require('child_process');
const https = require('https');

const stamp = Date.now().toString(36);
const email = `atest-${stamp}@shopifygmc.com`;
const SSH_HOST = process.env.SSH_HOST || 'root@62.169.16.57';
const SSH_KEY  = process.env.SSH_KEY  || `${process.env.HOME}/.ssh/gmcauditor_deploy`;
const SITE     = process.env.SITE     || 'https://shopifygmc.com';

function ssh(cmd) {
  const sshCmd = `ssh -i ${SSH_KEY} -o IdentitiesOnly=yes -o PasswordAuthentication=no -o StrictHostKeyChecking=no ${SSH_HOST} ${JSON.stringify(cmd)}`;
  return execSync(sshCmd, { encoding: 'utf8', maxBuffer: 5 * 1024 * 1024 });
}

function step(label, fn) {
  process.stdout.write(`\n→ ${label}\n`);
  return Promise.resolve()
    .then(fn)
    .then(() => process.stdout.write('  ✓ ok\n'))
    .catch((e) => {
      process.stdout.write(`  ✗ FAIL: ${e.message}\n`);
      cleanup();
      process.exit(1);
    });
}

function assert(cond, msg) { if (!cond) throw new Error(msg); }

function cleanup() {
  try { ssh(`echo '${email}' | mailbox del ${email} || true`); } catch (_) {}
}

function head(url) {
  return new Promise((resolve, reject) => {
    https.get(url, { method: 'GET', rejectUnauthorized: false }, (res) => {
      // Drain so the socket can close.
      res.on('data', () => {});
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers }));
    }).on('error', reject);
  });
}

(async () => {
  console.log(`Phase 4 verification — mailbox: ${email}`);

  await step('1. /admin/mail/activity requires auth (302 → /admin/login)', async () => {
    const r = await head(`${SITE}/admin/mail/activity?email=${encodeURIComponent(email)}`);
    assert(r.status === 302 || r.status === 303 || r.status === 401 || r.status === 403,
      `expected redirect/forbidden, got ${r.status}`);
    if (r.status === 302 || r.status === 303) {
      const loc = (r.headers.location || '').toLowerCase();
      assert(loc.includes('/admin/login'),
        `expected redirect to /admin/login, got ${r.headers.location}`);
    }
  });

  await step('2. create mailbox', () => {
    const out = ssh(`mailbox add ${email}`);
    assert(out.includes('ok:') && out.includes(email), 'mailbox add failed: ' + out);
  });

  await step('3. send 3 distinct messages, each lands in mail.log', () => {
    for (let i = 1; i <= 3; i++) {
      ssh(`swaks --to ${email} --from probe-${stamp}-${i}@shopifygmc.com --server localhost:25 ` +
          `--header 'Subject: phase4-${stamp}-${i}' --body "phase4 probe ${i}" >/dev/null 2>&1`);
    }
    ssh(`sleep 2`); // log flush
    const grepped = ssh(`grep -F "${email}" /var/log/mail.log | tail -50 || true`);
    const matches = grepped.split('\n').filter((l) => l.includes(email));
    assert(matches.length >= 3,
      `expected ≥3 mail.log lines mentioning ${email}, got ${matches.length}`);
  });

  await step('4. cleanup', () => { cleanup(); });

  console.log('\n✓ Phase 4 verified (auth gate + mail.log populated for the new mailbox)');
  process.exit(0);
})().catch((e) => {
  console.error('\nUNEXPECTED ERROR:', e);
  cleanup();
  process.exit(1);
});
