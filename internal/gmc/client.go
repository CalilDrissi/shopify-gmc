package gmc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ContentBaseURL is the production Content API root. Override via
// Client.BaseURL for tests.
const ContentBaseURL = "https://shoppingcontent.googleapis.com/content/v2.1/"

// TokenSupplier returns a fresh access token. The supplier owns whatever
// caching + refresh policy applies — Client just calls it before each
// request. Access tokens are never persisted by Client.
type TokenSupplier func(ctx context.Context) (string, error)

// Client is the typed wrapper around the Content API. Construct via NewClient.
//
// Errors:
//
//   - ErrUnauthorized — a 401 came back. Caller should mark the connection
//     `revoked` and notify the owner. Don't retry without re-consenting.
//   - ErrRateLimited  — exhausted retry budget after repeated 429s. Caller
//     should requeue the work for later.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Logger  *slog.Logger
	Token   TokenSupplier
	// MaxRetries on retryable errors (429, 5xx). Default 4 — combined with
	// the exponential backoff that hits ~1 hour at the upper end.
	MaxRetries int
}

var (
	ErrUnauthorized = errors.New("gmc: unauthorized (401)")
	ErrRateLimited  = errors.New("gmc: rate limited (429) — retry budget exhausted")
)

func NewClient(token TokenSupplier, logger *slog.Logger) *Client {
	return &Client{
		BaseURL:    ContentBaseURL,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		Logger:     logger,
		Token:      token,
		MaxRetries: 4,
	}
}

// ListAccounts returns the Merchant Center accounts the authenticated user
// has access to. Used by the connect flow to drive the picker (or auto-link).
//
// Authuser is "me" — Google's REST API supports both numeric account IDs
// and the literal "authinfo" path which returns scope+identifier info.
// We use accounts.authinfo since accounts.list requires a parent account ID
// that the user might not yet have.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	var resp struct {
		AccountIdentifiers []struct {
			MerchantID    string `json:"merchantId,omitempty"`
			AggregatorID  string `json:"aggregatorId,omitempty"`
		} `json:"accountIdentifiers"`
	}
	if err := c.do(ctx, "GET", "accounts/authinfo", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(resp.AccountIdentifiers))
	for _, a := range resp.AccountIdentifiers {
		id := a.MerchantID
		if id == "" {
			id = a.AggregatorID
		}
		if id == "" {
			continue
		}
		out = append(out, Account{ID: id, Name: "Merchant " + id})
	}
	return out, nil
}

// GetAccountStatus calls accountstatuses.get for one merchant.
func (c *Client) GetAccountStatus(ctx context.Context, merchantID string) (*AccountStatus, error) {
	var raw struct {
		MerchantID         string             `json:"merchantId"`
		WebsiteClaimed     bool               `json:"websiteClaimed"`
		AccountLevelIssues []AccountIssue     `json:"accountLevelIssues"`
		Products           []AccountProductStat `json:"products"`
	}
	path := fmt.Sprintf("%s/accountstatuses/%s", merchantID, merchantID)
	if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
		return nil, err
	}
	st := &AccountStatus{
		MerchantID:         raw.MerchantID,
		WebsiteClaimed:     raw.WebsiteClaimed,
		AccountLevelIssues: raw.AccountLevelIssues,
	}
	if len(raw.Products) > 0 {
		st.Products = raw.Products[0]
	}
	st.Status = synthesiseStatus(raw.AccountLevelIssues)
	return st, nil
}

func synthesiseStatus(issues []AccountIssue) string {
	worst := "active"
	for _, i := range issues {
		switch i.Severity {
		case "critical":
			return "suspended"
		case "error":
			worst = "warning"
		case "suggestion":
			if worst == "active" {
				worst = "warning"
			}
		}
	}
	return worst
}

// ListProductStatuses pages through productstatuses.list and returns every
// item. Each page is up to 250 items; large stores may take several requests.
func (c *Client) ListProductStatuses(ctx context.Context, merchantID string) ([]ProductStatus, error) {
	var all []ProductStatus
	pageToken := ""
	for {
		var raw struct {
			Resources     []ProductStatus `json:"resources"`
			NextPageToken string          `json:"nextPageToken,omitempty"`
		}
		path := fmt.Sprintf("%s/productstatuses?maxResults=250", merchantID)
		if pageToken != "" {
			path += "&pageToken=" + url.QueryEscape(pageToken)
		}
		if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
			return nil, err
		}
		all = append(all, raw.Resources...)
		if raw.NextPageToken == "" {
			break
		}
		pageToken = raw.NextPageToken
	}
	return all, nil
}

// GetDatafeedStatuses returns the processing status of every feed.
func (c *Client) GetDatafeedStatuses(ctx context.Context, merchantID string) ([]DatafeedStatus, error) {
	var raw struct {
		Resources []DatafeedStatus `json:"resources"`
	}
	path := fmt.Sprintf("%s/datafeedstatuses", merchantID)
	if err := c.do(ctx, "GET", path, nil, &raw); err != nil {
		return nil, err
	}
	return raw.Resources, nil
}

// ----------------------------------------------------------------------------
// Transport: bearer token, retry/backoff
// ----------------------------------------------------------------------------

// do executes a single request against the Content API with bearer auth +
// retry on 429/5xx. The caller's `out` is JSON-decoded only on a 2xx.
func (c *Client) do(ctx context.Context, method, relPath string, body io.Reader, out any) error {
	if c.MaxRetries == 0 {
		c.MaxRetries = 4
	}
	logger := c.Logger
	if logger == nil {
		logger = slog.Default()
	}

	tok, err := c.Token(ctx)
	if err != nil {
		return fmt.Errorf("gmc: token: %w", err)
	}
	full := strings.TrimRight(c.BaseURL, "/") + "/" + strings.TrimLeft(relPath, "/")

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, full, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if attempt == c.MaxRetries {
				return fmt.Errorf("gmc: %s %s: %w", method, relPath, err)
			}
			c.sleep(ctx, attempt, "")
			continue
		}

		switch {
		case resp.StatusCode == 200, resp.StatusCode == 201, resp.StatusCode == 204:
			err := json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			if err == io.EOF {
				return nil
			}
			return err

		case resp.StatusCode == 401, resp.StatusCode == 403:
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			logger.Warn("gmc_unauthorized", slog.String("path", relPath), slog.Int("status", resp.StatusCode), slog.String("body", trim(b, 256)))
			return ErrUnauthorized

		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			retryAfter := resp.Header.Get("Retry-After")
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("gmc: %s %s: status %d body=%s", method, relPath, resp.StatusCode, trim(b, 200))
			if attempt == c.MaxRetries {
				if resp.StatusCode == 429 {
					return ErrRateLimited
				}
				return lastErr
			}
			c.sleep(ctx, attempt, retryAfter)
			continue

		default:
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("gmc: %s %s: status %d body=%s", method, relPath, resp.StatusCode, trim(b, 200))
		}
	}
	return lastErr
}

// sleep waits attempt^2 * 30s (capped at 1h), respecting Retry-After
// headers when Google sends them. Cancels early if ctx is cancelled.
func (c *Client) sleep(ctx context.Context, attempt int, retryAfter string) {
	d := time.Duration(math.Pow(2, float64(attempt))) * 30 * time.Second
	d += time.Duration(rand.IntN(1000)) * time.Millisecond
	if d > time.Hour {
		d = time.Hour
	}
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			d = time.Duration(secs) * time.Second
			if d > time.Hour {
				d = time.Hour
			}
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func trim(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
