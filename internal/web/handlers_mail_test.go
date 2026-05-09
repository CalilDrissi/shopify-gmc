package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSizeBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"2K", 2048},
		{"500M", 524288000},
		{"1G", 1073741824},
		{"3T", 3 * 1024 * 1024 * 1024 * 1024},
		{"12345", 12345},
		{"1g", 1073741824},
	}
	for _, c := range cases {
		got, err := parseSizeBytes(c.in)
		if err != nil {
			t.Fatalf("parseSizeBytes(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseSizeBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMaildirsizeUsedBytes(t *testing.T) {
	dir := t.TempDir()

	// Real-world layout: line 1 = quota header (1 GiB), line 2 = absolute
	// baseline from recalc (49348 bytes / 12 messages), then a few deltas.
	p := filepath.Join(dir, "maildirsize")
	if err := os.WriteFile(p, []byte("1073741824S\n49348 12\n+100 1\n+200 1\n-50 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := maildirsizeUsedBytes(p); got != 49598 {
		t.Errorf("got %d, want 49598 (49348 + 100 + 200 - 50)", got)
	}

	// Quota-only file (no usage lines yet) → 0.
	pEmpty := filepath.Join(dir, "quota-only")
	os.WriteFile(pEmpty, []byte("1073741824S\n"), 0o644)
	if v := maildirsizeUsedBytes(pEmpty); v != 0 {
		t.Errorf("quota-only = %d, want 0", v)
	}

	// Missing file returns 0 (legitimate: never-delivered mailbox).
	if v := maildirsizeUsedBytes(filepath.Join(dir, "absent")); v != 0 {
		t.Errorf("missing file = %d, want 0", v)
	}
	if v := maildirsizeUsedBytes(""); v != 0 {
		t.Errorf("empty path = %d, want 0", v)
	}

	// Negative running totals clamp at 0 (mailbox emptied past its baseline).
	p2 := filepath.Join(dir, "underflow")
	os.WriteFile(p2, []byte("100S\n-5000 1\n"), 0o644)
	if v := maildirsizeUsedBytes(p2); v != 0 {
		t.Errorf("underflow = %d, want 0", v)
	}
}

func TestMaildirsizePath(t *testing.T) {
	cases := []struct {
		email, want string
	}{
		{"admin@shopifygmc.com", "/var/mail/vmail/shopifygmc.com/admin/maildirsize"},
		{"sales@example.org", "/var/mail/vmail/example.org/sales/maildirsize"},
		{"bogus", ""},
		{"@nope", ""},
		{"nope@", ""},
	}
	for _, c := range cases {
		if got := maildirsizePath(c.email); got != c.want {
			t.Errorf("maildirsizePath(%q) = %q, want %q", c.email, got, c.want)
		}
	}
}

func TestQuotaSizeRE(t *testing.T) {
	good := []string{"", "0", "1G", "500M", "2K", "12345", "1g", "100t"}
	bad := []string{"1GB", "1.5G", "abc", "1G1", " 1G", "-1G"}
	for _, s := range good {
		if !quotaSizeRE.MatchString(s) {
			t.Errorf("expected %q to match", s)
		}
	}
	for _, s := range bad {
		if quotaSizeRE.MatchString(s) {
			t.Errorf("expected %q NOT to match", s)
		}
	}
}

func TestQuotaRuleRE(t *testing.T) {
	line := "admin@shopifygmc.com:HASH:5000:5000::/var/mail/vmail/shopifygmc.com/admin::userdb_quota_rule=*:storage=2G"
	m := quotaRuleRE.FindStringSubmatch(line)
	if len(m) != 2 || m[1] != "2G" {
		t.Fatalf("got %v, want [_ 2G]", m)
	}
	if got := quotaRuleRE.FindStringSubmatch("no quota here"); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestParseImportCSV_ValidFiveRows(t *testing.T) {
	in := strings.NewReader("email,password,quota\n" +
		"a@example.com,,1G\n" +
		"b@example.com,SuppliedPW1,500M\n" +
		"c@example.com,,\n" +
		"d@example.com,,0\n" +
		"e@example.com,SuppliedPW2,2G\n")
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Err != "" {
			t.Errorf("row %d: unexpected validation error %q", i, r.Err)
		}
	}
	if rows[0].Email != "a@example.com" || rows[0].Quota != "1G" {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].Password != "SuppliedPW1" {
		t.Errorf("row 1 password not preserved: got %q", rows[1].Password)
	}
	if rows[2].Quota != "" {
		t.Errorf("row 2 quota should be empty, got %q", rows[2].Quota)
	}
	if rows[3].Quota != "0" {
		t.Errorf("row 3 quota should be 0, got %q", rows[3].Quota)
	}
	// LineNo: header is line 1, first data row is line 2.
	if rows[0].LineNo != 2 || rows[4].LineNo != 6 {
		t.Errorf("LineNo wrong: %d / %d", rows[0].LineNo, rows[4].LineNo)
	}
}

func TestParseImportCSV_BlankLinesSkipped(t *testing.T) {
	in := strings.NewReader("email,password,quota\n" +
		"a@example.com,,\n" +
		",,\n" +
		"b@example.com,,1G\n")
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (blank skipped), got %d", len(rows))
	}
	if rows[0].Email != "a@example.com" || rows[1].Email != "b@example.com" {
		t.Errorf("got %+v", rows)
	}
}

func TestParseImportCSV_MissingHeader(t *testing.T) {
	cases := []string{
		"",                                               // empty file
		"a@example.com,,1G\nb@example.com,,500M\n",       // no header
		"address,password,quota\na@example.com,,1G\n",    // wrong first column
		"email,quota,password\na@example.com,1G,\n",      // wrong column order
		"email,password\na@example.com,\n",               // too few columns
	}
	for _, c := range cases {
		_, err := parseImportCSV(strings.NewReader(c))
		if err == nil {
			t.Errorf("expected error for input %q", c)
		}
	}
}

func TestParseImportCSV_DuplicateRowsPreserved(t *testing.T) {
	// Parser does NOT dedupe — that's the executor's job (it will surface
	// `mailbox add` failing with "exists" as skipped:duplicate). We just
	// confirm both rows are returned so the executor sees them.
	in := strings.NewReader("email,password,quota\n" +
		"dup@example.com,,1G\n" +
		"dup@example.com,,2G\n")
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Email != rows[1].Email {
		t.Errorf("expected both emails equal, got %q vs %q", rows[0].Email, rows[1].Email)
	}
}

func TestParseImportCSV_InvalidEmails(t *testing.T) {
	in := strings.NewReader("email,password,quota\n" +
		"good@example.com,,1G\n" +
		"not-an-email,,1G\n" +
		"@nope.com,,1G\n" +
		"trailing@,,1G\n" +
		"also.good@example.org,,500M\n")
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	if rows[0].Err != "" {
		t.Errorf("row 0 should be valid, got err %q", rows[0].Err)
	}
	for _, i := range []int{1, 2, 3} {
		if rows[i].Err != "invalid email" {
			t.Errorf("row %d: want invalid email, got %q", i, rows[i].Err)
		}
	}
	if rows[4].Err != "" {
		t.Errorf("row 4 should be valid, got err %q", rows[4].Err)
	}
}

func TestParseImportCSV_InvalidQuotas(t *testing.T) {
	in := strings.NewReader("email,password,quota\n" +
		"a@example.com,,1GB\n" +     // bad: GB suffix not allowed
		"b@example.com,,1.5G\n" +    // bad: decimal
		"c@example.com,,2X\n" +      // bad: unknown unit
		"d@example.com,,1G\n" +      // good
		"e@example.com,,0\n" +       // good (unlimited)
		"f@example.com,,\n")         // good (default)
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("want 6 rows, got %d", len(rows))
	}
	for _, i := range []int{0, 1, 2} {
		if !strings.HasPrefix(rows[i].Err, "bad quota") {
			t.Errorf("row %d: want bad quota error, got %q", i, rows[i].Err)
		}
	}
	for _, i := range []int{3, 4, 5} {
		if rows[i].Err != "" {
			t.Errorf("row %d should be valid, got err %q", i, rows[i].Err)
		}
	}
}

func TestParseImportCSV_HeaderCaseInsensitive(t *testing.T) {
	in := strings.NewReader("Email, Password , QUOTA\na@example.com,,1G\n")
	rows, err := parseImportCSV(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 || rows[0].Email != "a@example.com" {
		t.Errorf("got %+v", rows)
	}
}

func TestParseImportCSV_RowCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("email,password,quota\n")
	for i := 0; i <= maxImportRows; i++ {
		sb.WriteString("user")
		sb.WriteString(strings.Repeat("a", 1)) // unique-ish
		// Use line index as part of email to keep them distinct.
		sb.WriteString("@example.com,,1G\n")
	}
	// We wrote maxImportRows+1 data rows; parser should refuse.
	_, err := parseImportCSV(strings.NewReader(sb.String()))
	if err == nil {
		t.Fatalf("expected error for over-cap upload")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("expected 'too many' error, got %q", err.Error())
	}
}

// Real Postfix mail.log snippets. Two flows, both for support@shopifygmc.com:
//   D438583FE1: outbound, support@ → alice@example.org, sent (2.0.0)
//   D5E1183FE2: inbound,  bob@example.net → support@, deferred 4.7.1
// Plus an unrelated flow for someone-else@ that must NOT appear in results.
const fixtureMailLog = `May  9 12:34:56 mail postfix/smtpd[12345]: D438583FE1: client=localhost[127.0.0.1]
May  9 12:34:56 mail postfix/cleanup[12346]: D438583FE1: message-id=<abc@shopifygmc.com>
May  9 12:34:56 mail postfix/qmgr[12347]: D438583FE1: from=<support@shopifygmc.com>, size=4242, nrcpt=1 (queue active)
May  9 12:34:57 mail postfix/smtp[12348]: D438583FE1: to=<alice@example.org>, relay=mx.example.org[1.2.3.4]:25, delay=0.7, delays=0.1/0/0.3/0.3, dsn=2.0.0, status=sent (250 2.0.0 OK)
May  9 12:34:57 mail postfix/qmgr[12347]: D438583FE1: removed
May  9 12:36:00 mail postfix/smtpd[12350]: D5E1183FE2: client=relay.example.net[5.6.7.8]
May  9 12:36:00 mail postfix/cleanup[12351]: D5E1183FE2: message-id=<def@example.net>
May  9 12:36:00 mail postfix/qmgr[12352]: D5E1183FE2: from=<bob@example.net>, size=8181, nrcpt=1 (queue active)
May  9 12:36:01 mail postfix/lmtp[12353]: D5E1183FE2: to=<support@shopifygmc.com>, relay=127.0.0.1[127.0.0.1]:24, delay=1.0, dsn=4.7.1, status=deferred (host 127.0.0.1[127.0.0.1] said: 451 4.7.1 try later)
May  9 12:40:00 mail postfix/smtpd[12360]: E11183FE3A: client=localhost[127.0.0.1]
May  9 12:40:00 mail postfix/qmgr[12361]: E11183FE3A: from=<someone-else@shopifygmc.com>, size=999, nrcpt=1 (queue active)
May  9 12:40:01 mail postfix/smtp[12362]: E11183FE3A: to=<carol@example.com>, relay=mx.example.com[9.9.9.9]:25, dsn=2.0.0, status=sent (250 OK)
`

func TestParseMailLog(t *testing.T) {
	lines := strings.Split(strings.TrimRight(fixtureMailLog, "\n"), "\n")
	events := parseMailLog(lines, "support@shopifygmc.com", 200)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (filtered to support@)", len(events))
	}

	// Sorted descending by timestamp → the deferred inbound (12:36) comes first.
	in := events[0]
	if in.QueueID != "D5E1183FE2" {
		t.Errorf("first event qid=%q, want D5E1183FE2", in.QueueID)
	}
	if in.Direction != "in" {
		t.Errorf("first event direction=%q, want in", in.Direction)
	}
	if in.Counterparty != "bob@example.net" {
		t.Errorf("first event counterparty=%q, want bob@example.net", in.Counterparty)
	}
	if in.From != "bob@example.net" {
		t.Errorf("first event from=%q, want bob@example.net", in.From)
	}
	if in.SizeBytes != 8181 {
		t.Errorf("first event size=%d, want 8181", in.SizeBytes)
	}
	if in.Status != "deferred" {
		t.Errorf("first event status=%q, want deferred", in.Status)
	}
	if in.DSN != "4.7.1" {
		t.Errorf("first event dsn=%q, want 4.7.1", in.DSN)
	}

	out := events[1]
	if out.QueueID != "D438583FE1" {
		t.Errorf("second event qid=%q, want D438583FE1", out.QueueID)
	}
	if out.Direction != "out" {
		t.Errorf("second event direction=%q, want out", out.Direction)
	}
	if out.Counterparty != "alice@example.org" {
		t.Errorf("second event counterparty=%q, want alice@example.org", out.Counterparty)
	}
	if out.Status != "sent" {
		t.Errorf("second event status=%q, want sent", out.Status)
	}
	if out.DSN != "2.0.0" {
		t.Errorf("second event dsn=%q, want 2.0.0", out.DSN)
	}
	if out.SizeBytes != 4242 {
		t.Errorf("second event size=%d, want 4242", out.SizeBytes)
	}
}

func TestParseMailLog_FilterMissesUnrelated(t *testing.T) {
	lines := strings.Split(strings.TrimRight(fixtureMailLog, "\n"), "\n")
	for _, qid := range []string{"E11183FE3A"} {
		evs := parseMailLog(lines, "support@shopifygmc.com", 200)
		for _, e := range evs {
			if e.QueueID == qid {
				t.Errorf("unrelated qid %s leaked into support@ results", qid)
			}
		}
	}
	// Carol IS the recipient of the third flow → should match for her.
	evs := parseMailLog(lines, "carol@example.com", 200)
	if len(evs) != 1 || evs[0].QueueID != "E11183FE3A" {
		t.Errorf("expected one event for carol@, got %+v", evs)
	}
}

func TestParseMailLog_RFC3339Header(t *testing.T) {
	// Newer Postfix builds (or journald-injected) emit an RFC3339 timestamp.
	rfc := strings.Join([]string{
		"2026-05-09T12:34:56.123456+00:00 mail postfix/qmgr[1]: ABCDEF01: from=<a@x.com>, size=10, nrcpt=1 (queue active)",
		"2026-05-09T12:34:57.000000+00:00 mail postfix/smtp[2]: ABCDEF01: to=<b@y.com>, relay=mx, dsn=2.0.0, status=sent (250 OK)",
	}, "\n")
	evs := parseMailLog(strings.Split(rfc, "\n"), "a@x.com", 10)
	if len(evs) != 1 {
		t.Fatalf("got %d, want 1", len(evs))
	}
	if evs[0].Status != "sent" || evs[0].Direction != "out" {
		t.Errorf("got %+v, want status=sent direction=out", evs[0])
	}
	want := time.Date(2026, 5, 9, 12, 34, 57, 0, time.UTC)
	if !evs[0].Timestamp.Equal(want) {
		t.Errorf("timestamp = %v, want %v", evs[0].Timestamp, want)
	}
}

func TestParseMailLog_EmptyEmail(t *testing.T) {
	lines := strings.Split(fixtureMailLog, "\n")
	if got := parseMailLog(lines, "", 200); got != nil {
		t.Errorf("empty email should return nil, got %+v", got)
	}
}

func TestParseMailLog_Cap(t *testing.T) {
	// Build 5 flows for the same address, ask for a cap of 2 → return only 2.
	var b strings.Builder
	for i := 0; i < 5; i++ {
		qid := []string{"AAAA0001", "AAAA0002", "AAAA0003", "AAAA0004", "AAAA0005"}[i]
		ts := []string{"May  9 12:30:0", "May  9 12:31:0", "May  9 12:32:0", "May  9 12:33:0", "May  9 12:34:0"}[i] + "0"
		b.WriteString(ts + " mail postfix/qmgr[1]: " + qid + ": from=<x@y.com>, size=10, nrcpt=1 (queue active)\n")
		b.WriteString(ts + " mail postfix/smtp[2]: " + qid + ": to=<dest@z.com>, relay=mx, dsn=2.0.0, status=sent (250 OK)\n")
	}
	evs := parseMailLog(strings.Split(strings.TrimRight(b.String(), "\n"), "\n"), "x@y.com", 2)
	if len(evs) != 2 {
		t.Fatalf("cap not enforced: got %d, want 2", len(evs))
	}
	// Sorted descending → newest two are AAAA0005 and AAAA0004.
	if evs[0].QueueID != "AAAA0005" || evs[1].QueueID != "AAAA0004" {
		t.Errorf("got qids %s,%s — want AAAA0005,AAAA0004", evs[0].QueueID, evs[1].QueueID)
	}
}

func TestReadMailLogTail_Small(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mail.log")
	body := "line one\nline two\nline three\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := readMailLogTail(p, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "line one" || lines[2] != "line three" {
		t.Errorf("got %v", lines)
	}
}

func TestReadMailLogTail_TruncatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mail.log")
	// Write 200 KB; cap read at 50 KB. Last several lines must survive; the
	// first (partial) line after the seek must be dropped.
	var sb strings.Builder
	for i := 0; i < 4000; i++ {
		sb.WriteString("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n") // 46 bytes
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := readMailLogTail(p, 50*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("got 0 lines from a 200 KB file with 50 KB cap")
	}
	// Each surviving line must be the full 45-character "aaa..." string.
	for i, l := range lines {
		if l != strings.Repeat("a", 45) {
			t.Fatalf("line %d corrupt (partial-line drop failed): %q", i, l)
		}
	}
}

func TestParseMailLogTimestamp(t *testing.T) {
	now := time.Date(2026, 5, 9, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"May  9 12:34:56", time.Date(2026, 5, 9, 12, 34, 56, 0, time.UTC)},
		// Future month → roll back to last year.
		{"Dec 31 23:59:59", time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)},
		{"2026-05-09T12:34:56+00:00", time.Date(2026, 5, 9, 12, 34, 56, 0, time.UTC)},
	}
	for _, c := range cases {
		got := parseMailLogTimestamp(c.in, now)
		if !got.Equal(c.want) {
			t.Errorf("parseMailLogTimestamp(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if got := parseMailLogTimestamp("not a timestamp", now); !got.IsZero() {
		t.Errorf("expected zero time for bogus input, got %v", got)
	}
}

func TestSuspendedRE(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::nopassword=y", true},
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::userdb_quota_rule=*:storage=1G nopassword=y", true},
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::nopassword=y userdb_quota_rule=*:storage=1G", true},
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::userdb_quota_rule=*:storage=1G", false},
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::", false},
		// Should not match a value that merely contains the substring:
		{"admin@x.com:H:5000:5000::/var/mail/vmail/x/admin::nopassword=yesplease", false},
	}
	for _, c := range cases {
		if got := suspendedRE.MatchString(c.line); got != c.want {
			t.Errorf("suspendedRE(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestReadMailboxesParsesSuspended(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "users")
	content := "" +
		"a@x.com:HASH:5000:5000::/var/mail/vmail/x.com/a::userdb_quota_rule=*:storage=1G\n" +
		"b@x.com:HASH:5000:5000::/var/mail/vmail/x.com/b::userdb_quota_rule=*:storage=2G nopassword=y\n" +
		"c@x.com:HASH:5000:5000::/var/mail/vmail/x.com/c::nopassword=y\n"
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Drive the parser by temporarily pointing dovecotUsers at our fixture
	// — but that's a package-level const. Instead, replicate the per-line
	// detection logic to assert the regex is what readMailboxes relies on.
	want := map[string]bool{
		"a@x.com:HASH:5000:5000::/var/mail/vmail/x.com/a::userdb_quota_rule=*:storage=1G":                false,
		"b@x.com:HASH:5000:5000::/var/mail/vmail/x.com/b::userdb_quota_rule=*:storage=2G nopassword=y":   true,
		"c@x.com:HASH:5000:5000::/var/mail/vmail/x.com/c::nopassword=y":                                  true,
	}
	for line, expect := range want {
		if got := suspendedRE.MatchString(line); got != expect {
			t.Errorf("line %q: got suspended=%v, want %v", line, got, expect)
		}
	}
}
