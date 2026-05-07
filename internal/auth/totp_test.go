package auth

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateTOTP_Format(t *testing.T) {
	t.Parallel()
	setup, err := GenerateTOTP("gmcauditor", "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if setup.Secret == "" {
		t.Error("secret empty")
	}
	if !strings.HasPrefix(setup.URL, "otpauth://totp/") {
		t.Errorf("url=%q does not start with otpauth://totp/", setup.URL)
	}
	if !strings.Contains(setup.URL, "issuer=gmcauditor") {
		t.Errorf("url=%q missing issuer", setup.URL)
	}
}

func TestValidateTOTP_RoundTrip(t *testing.T) {
	t.Parallel()
	setup, err := GenerateTOTP("gmcauditor", "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	at := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	code, err := GenerateTOTPCodeAt(setup.Secret, at)
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code len=%d, want 6", len(code))
	}

	ok, err := ValidateTOTPAt(setup.Secret, code, at)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !ok {
		t.Error("expected validation ok at t0")
	}

	okSkew, err := ValidateTOTPAt(setup.Secret, code, at.Add(15*time.Second))
	if err != nil {
		t.Fatalf("validate skew: %v", err)
	}
	if !okSkew {
		t.Error("expected validation ok within window")
	}

	okFar, err := ValidateTOTPAt(setup.Secret, code, at.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("validate far: %v", err)
	}
	if okFar {
		t.Error("code should not validate 5 minutes later")
	}

	badCode := "000000"
	if code == badCode {
		badCode = "999999"
	}
	bad, err := ValidateTOTPAt(setup.Secret, badCode, at)
	if err != nil {
		t.Fatalf("validate bad: %v", err)
	}
	if bad {
		t.Error("clearly wrong code should not validate")
	}
}

func TestTOTPQRPNG(t *testing.T) {
	t.Parallel()
	setup, err := GenerateTOTP("gmcauditor", "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	png, err := TOTPQRPNG(setup, 200, 200)
	if err != nil {
		t.Fatalf("png: %v", err)
	}
	if len(png) < 100 {
		t.Errorf("png too small: %d bytes", len(png))
	}
	if string(png[1:4]) != "PNG" {
		t.Errorf("not a PNG file (header=%q)", png[:8])
	}
}
