package web

import (
	"os"
	"path/filepath"
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
