package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifySignature returns true when the supplied signature header matches an
// HMAC-SHA256 of the raw request body computed with the shared secret.
//
// Gumroad sends the signature as a hex-encoded digest in the X-Gumroad-Signature
// header. Using hmac.Equal guards against timing attacks.
func VerifySignature(secret, body []byte, signatureHex string) bool {
	if len(secret) == 0 || signatureHex == "" {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// SignBody is provided so tests (and the dev fixtures used in flows) can
// compute the same digest the server expects. Not used in the production
// HTTP path.
func SignBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
