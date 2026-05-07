package auth

import (
	"strings"
	"testing"
)

func TestHashPassword_FormatAndUniqueness(t *testing.T) {
	t.Parallel()

	h1, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash 1: %v", err)
	}
	h2, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash 2: %v", err)
	}

	if h1 == h2 {
		t.Errorf("two hashes of the same password must differ (random salt)")
	}
	if !strings.HasPrefix(h1, "$argon2id$") {
		t.Errorf("hash missing $argon2id$ prefix: %q", h1)
	}
	if !strings.Contains(h1, "m=65536,t=3,p=4") {
		t.Errorf("hash missing expected parameters m=65536,t=3,p=4: %q", h1)
	}
}

func TestVerifyPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	cases := []struct {
		name     string
		password string
		want     bool
	}{
		{"matching", "hunter2", true},
		{"mismatching", "hunter3", false},
		{"empty", "", false},
		{"prefix-attack", "hunter", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := VerifyPassword(tc.password, hash)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if ok != tc.want {
				t.Errorf("got %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestVerifyPassword_MalformedHash(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=65536,t=3,p=4$YWFhYWFhYWFhYWFhYWFhYQ$YWFhYQ",
		"$argon2id$v=19$m=65536,t=3,p=4$YWFhYWFhYWFhYWFhYWFhYQ",
		"$argon2id$$$$",
	}
	for _, c := range cases {
		ok, err := VerifyPassword("anything", c)
		if err == nil {
			t.Errorf("expected error for %q, got ok=%v", c, ok)
		}
	}
}
