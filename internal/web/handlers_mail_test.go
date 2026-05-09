package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
