package auth

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

var ErrTOTPInvalid = errors.New("auth: totp code invalid")

type TOTPSetup struct {
	Key    *otp.Key
	Secret string
	URL    string
}

func GenerateTOTP(issuer, accountName string) (TOTPSetup, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
	})
	if err != nil {
		return TOTPSetup{}, fmt.Errorf("totp generate: %w", err)
	}
	return TOTPSetup{
		Key:    key,
		Secret: key.Secret(),
		URL:    key.URL(),
	}, nil
}

func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

func ValidateTOTPAt(secret, code string, t time.Time) (bool, error) {
	return totp.ValidateCustom(code, secret, t, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
}

func GenerateTOTPCodeAt(secret string, t time.Time) (string, error) {
	return totp.GenerateCode(secret, t)
}

func TOTPQRPNG(setup TOTPSetup, width, height int) ([]byte, error) {
	if setup.Key == nil {
		return nil, errors.New("auth: totp setup has no key")
	}
	img, err := setup.Key.Image(width, height)
	if err != nil {
		return nil, fmt.Errorf("totp image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode: %w", err)
	}
	return buf.Bytes(), nil
}
