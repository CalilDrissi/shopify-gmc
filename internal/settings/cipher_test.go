package settings

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	c, err := NewCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewCipher_KeyValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		key     string
		wantErr error
	}{
		{"empty", "", ErrEncryptionKeyMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCipher(tc.key)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got %v, want %v", err, tc.wantErr)
			}
		})
	}

	t.Run("not-base64", func(t *testing.T) {
		_, err := NewCipher("not!base64!!!")
		if err == nil {
			t.Fatal("expected error for non-base64")
		}
	})
	t.Run("wrong-length", func(t *testing.T) {
		_, err := NewCipher(base64.StdEncoding.EncodeToString([]byte("only-8-bytes")))
		if err == nil || !strings.Contains(err.Error(), "32 bytes") {
			t.Errorf("got %v, want 32 bytes error", err)
		}
	})
}

func TestCipher_RoundTrip(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)

	cases := []struct {
		name      string
		plaintext string
	}{
		{"empty", ""},
		{"short", "hi"},
		{"unicode", "héllo 🌍"},
		{"long", strings.Repeat("abcdef", 1000)},
		{"binary-like", string([]byte{0, 1, 2, 3, 0xff, 0xfe})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blob, err := c.Encrypt([]byte(tc.plaintext))
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			pt, err := c.Decrypt(blob)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if string(pt) != tc.plaintext {
				t.Errorf("round-trip mismatch")
			}
		})
	}
}

func TestCipher_NoncesDifferPerWrite(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	a, _ := c.Encrypt([]byte("hello"))
	b, _ := c.Encrypt([]byte("hello"))
	if string(a) == string(b) {
		t.Error("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestCipher_RejectsTamperAndShort(t *testing.T) {
	t.Parallel()
	c := newTestCipher(t)
	blob, _ := c.Encrypt([]byte("payload"))

	t.Run("flip-byte", func(t *testing.T) {
		bad := append([]byte(nil), blob...)
		bad[len(bad)-1] ^= 0x01
		_, err := c.Decrypt(bad)
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Errorf("got %v, want ErrInvalidCiphertext", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		_, err := c.Decrypt(blob[:5])
		if !errors.Is(err, ErrInvalidCiphertext) {
			t.Errorf("got %v, want ErrInvalidCiphertext", err)
		}
	})
}

func TestCipher_DifferentKeyFails(t *testing.T) {
	t.Parallel()
	a := newTestCipher(t)
	b := newTestCipher(t)
	blob, _ := a.Encrypt([]byte("secret"))
	_, err := b.Decrypt(blob)
	if !errors.Is(err, ErrInvalidCiphertext) {
		t.Errorf("got %v, want ErrInvalidCiphertext", err)
	}
}

func TestNewCipherFromEnv(t *testing.T) {
	t.Setenv(KeyEnvVar, "")
	if _, err := NewCipherFromEnv(); !errors.Is(err, ErrEncryptionKeyMissing) {
		t.Errorf("missing env: got %v, want ErrEncryptionKeyMissing", err)
	}
	if err := MustHaveKey(); !errors.Is(err, ErrEncryptionKeyMissing) {
		t.Errorf("MustHaveKey missing: got %v", err)
	}

	key := make([]byte, 32)
	rand.Read(key)
	t.Setenv(KeyEnvVar, base64.StdEncoding.EncodeToString(key))
	if _, err := NewCipherFromEnv(); err != nil {
		t.Errorf("with env: got %v", err)
	}
	if err := MustHaveKey(); err != nil {
		t.Errorf("MustHaveKey present: got %v", err)
	}
}
