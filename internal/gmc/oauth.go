package gmc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth holds the credentials and endpoints for the Google OAuth flow.
//
// We talk to the bare REST endpoints (token/revoke) rather than depend on
// golang.org/x/oauth2 — it's a few dozen lines of JSON-form glue and keeps
// the dep tree small.
type OAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string

	AuthURL   string // default: https://accounts.google.com/o/oauth2/v2/auth
	TokenURL  string // default: https://oauth2.googleapis.com/token
	RevokeURL string // default: https://oauth2.googleapis.com/revoke

	HTTP *http.Client
}

// Scope is the single Content-API scope the spec requires.
const Scope = "https://www.googleapis.com/auth/content"

func (o *OAuth) authURL() string {
	if o.AuthURL != "" {
		return o.AuthURL
	}
	return "https://accounts.google.com/o/oauth2/v2/auth"
}
func (o *OAuth) tokenURL() string {
	if o.TokenURL != "" {
		return o.TokenURL
	}
	return "https://oauth2.googleapis.com/token"
}
func (o *OAuth) revokeURL() string {
	if o.RevokeURL != "" {
		return o.RevokeURL
	}
	return "https://oauth2.googleapis.com/revoke"
}
func (o *OAuth) httpClient() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// AuthCodeURL builds the consent-screen URL the user is redirected to.
// access_type=offline + prompt=consent guarantees Google issues a refresh
// token (without prompt=consent, repeat connects from the same user only
// return an access token).
func (o *OAuth) AuthCodeURL(state string) string {
	v := url.Values{}
	v.Set("client_id", o.ClientID)
	v.Set("redirect_uri", o.RedirectURL)
	v.Set("response_type", "code")
	v.Set("scope", Scope)
	v.Set("access_type", "offline")
	v.Set("prompt", "consent")
	v.Set("include_granted_scopes", "true")
	v.Set("state", state)
	return o.authURL() + "?" + v.Encode()
}

// Exchange swaps an authorization code for access + refresh tokens.
func (o *OAuth) Exchange(ctx context.Context, code string) (*Token, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", o.ClientID)
	form.Set("client_secret", o.ClientSecret)
	form.Set("redirect_uri", o.RedirectURL)
	form.Set("grant_type", "authorization_code")
	return o.postToken(ctx, form)
}

// Refresh trades a refresh token for a new access token. Google does not
// return a refresh token on this call (only on the initial Exchange), so
// callers should keep the original refresh token in storage.
func (o *OAuth) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", o.ClientID)
	form.Set("client_secret", o.ClientSecret)
	form.Set("grant_type", "refresh_token")
	return o.postToken(ctx, form)
}

// Revoke at the OAuth server. Failure here is logged and ignored by callers;
// even if the revoke fails we still wipe our local copy.
func (o *OAuth) Revoke(ctx context.Context, token string) error {
	form := url.Values{}
	form.Set("token", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.revokeURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gmc: revoke status %d", resp.StatusCode)
	}
	return nil
}

func (o *OAuth) postToken(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := o.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("gmc: token POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var ge struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&ge)
		return nil, fmt.Errorf("gmc: token error %d: %s (%s)", resp.StatusCode, ge.Error, ge.ErrorDescription)
	}

	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("gmc: decode token: %w", err)
	}
	if raw.AccessToken == "" {
		return nil, errors.New("gmc: token response missing access_token")
	}
	return &Token{
		AccessToken:  raw.AccessToken,
		RefreshToken: raw.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second),
		Scope:        raw.Scope,
	}, nil
}
