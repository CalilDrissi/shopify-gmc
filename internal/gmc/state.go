package gmc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OAuthState is the payload we sign and pass through Google's `state`
// parameter. Carries everything the callback needs to identify the
// originating connect-click without trusting the redirect query string.
//
//   - SessionID  → must match the cookie at callback time (CSRF binding)
//   - TenantID   → which workspace owns this store
//   - StoreID    → which store to attach the connection to
//   - Nonce      → blind random for replay protection
//   - IssuedAt   → expire after 10 minutes
type OAuthState struct {
	SessionID string    `json:"sid"`
	TenantID  uuid.UUID `json:"t"`
	StoreID   uuid.UUID `json:"st"`
	UserID    uuid.UUID `json:"u"`
	Nonce     string    `json:"n"`
	IssuedAt  int64     `json:"i"`
}

const stateTTL = 10 * time.Minute

// SignOAuthState returns a base64url(payload).base64url(hmac) string the
// frontend never needs to introspect.
func SignOAuthState(secret []byte, st OAuthState) string {
	if st.IssuedAt == 0 {
		st.IssuedAt = time.Now().Unix()
	}
	if st.Nonce == "" {
		st.Nonce = newNonce()
	}
	body, _ := json.Marshal(st)
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(bodyB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bodyB64 + "." + sig
}

// VerifyOAuthState validates the HMAC, decodes the payload, and rejects
// anything older than stateTTL.
func VerifyOAuthState(secret []byte, token string) (OAuthState, error) {
	dot := -1
	for i, c := range token {
		if c == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return OAuthState{}, errors.New("oauth state: malformed token")
	}
	body, sig := token[:dot], token[dot+1:]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return OAuthState{}, errors.New("oauth state: bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return OAuthState{}, fmt.Errorf("oauth state: decode: %w", err)
	}
	var st OAuthState
	if err := json.Unmarshal(raw, &st); err != nil {
		return OAuthState{}, fmt.Errorf("oauth state: unmarshal: %w", err)
	}
	if time.Since(time.Unix(st.IssuedAt, 0)) > stateTTL {
		return OAuthState{}, errors.New("oauth state: expired")
	}
	return st, nil
}

func newNonce() string {
	var b [9]byte
	for i := range b {
		// Cheap nonce — combined with HMAC + IssuedAt this is plenty.
		b[i] = byte(time.Now().UnixNano() >> uint(i*8))
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
