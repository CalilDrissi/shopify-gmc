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
	p := filepath.Join(dir, "maildirsize")
	if err := os.WriteFile(p, []byte("12345S\n+100 1\n+200 1\n-50 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := maildirsizeUsedBytes(p)
	if got != 12595 {
		t.Errorf("got %d, want 12595", got)
	}

	// Missing file returns 0 (legitimate: never-delivered mailbox)
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
