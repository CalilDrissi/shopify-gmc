// Package monitoring evaluates alert subscriptions after every audit and
// dispatches one email per (subscription, trigger) that fires.
//
// Trigger rules:
//
//   - new_critical          — diff.new_critical_count > 0     (always sends)
//   - score_drop            — score_delta <= -threshold        (24h-rate-limited)
//   - audit_failed          — audit.status='failed'            (always sends)
//   - on_gmc_account_change — fired by the GMC sync (out of scope here)
//
// Dedupe within an audit is enforced by a UNIQUE index on
// (audit_id, user_id, trigger). Rate limiting (1 alert per user per store
// per 24h, except the always-send triggers) is enforced by a query on
// alert_dispatches before sending.
package monitoring

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/audit/differ"
	"github.com/example/gmcauditor/internal/mailer"
)

// Trigger names — keep in sync with the schema's `on_*` columns and with
// alert_dispatches.trigger values.
const (
	TriggerNewCritical = "new_critical"
	TriggerScoreDrop   = "score_drop"
	TriggerAuditFailed = "audit_failed"
	TriggerGMCChange   = "gmc_account_change"
)

// alwaysSend triggers skip the 24h rate limit. Critical issues and outright
// audit failures are always-on safety alerts; opting out is via subscription
// flag, not silently throttled.
var alwaysSend = map[string]bool{
	TriggerNewCritical: true,
	TriggerAuditFailed: true,
}

// Dispatcher fires alerts for one audit. Construct once at boot and call
// Dispatch from the worker AfterCommit hook.
type Dispatcher struct {
	Pool      *pgxpool.Pool
	Mailer    mailer.Mailer
	BaseURL   string
	MailFrom  string
	AppSecret []byte // signs unsubscribe URLs
	Logger    *slog.Logger
}

// AuditResult is the slim view of the just-finished audit the dispatcher
// needs. Builds from the worker's AuditOutput (we don't import audit here
// to avoid a cycle through differ).
type AuditResult struct {
	AuditID    uuid.UUID
	TenantID   uuid.UUID
	StoreID    uuid.UUID
	StoreName  string
	Status     string // "succeeded" or "failed"
	Score      int
	RiskLevel  string
	ErrorMsg   string
}

// Dispatch evaluates subscriptions for the store and sends one email per
// (subscription, trigger) that fires. Errors per-subscription are logged
// and swallowed — a flaky SMTP shouldn't crash the worker.
func (d *Dispatcher) Dispatch(ctx context.Context, res AuditResult, diff *differ.Diff) {
	if d == nil || d.Pool == nil {
		return
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	triggered := d.fired(res, diff)
	if len(triggered) == 0 {
		d.Logger.Debug("alerts_no_triggers",
			slog.String("audit_id", res.AuditID.String()))
		return
	}

	subs, err := d.loadSubscriptions(ctx, res.TenantID, res.StoreID)
	if err != nil {
		d.Logger.Warn("alerts_load_subs", slog.Any("err", err))
		return
	}
	for _, s := range subs {
		for _, trig := range triggered {
			if !s.subscribesTo(trig) {
				continue
			}
			if !alwaysSend[trig] {
				rateLimited, err := d.recentlyAlerted(ctx, s.UserID, res.StoreID)
				if err != nil {
					d.Logger.Warn("alerts_rate_lookup", slog.Any("err", err))
				}
				if rateLimited {
					d.Logger.Info("alerts_rate_limited",
						slog.String("trigger", trig),
						slog.String("user_id", s.UserID.String()),
						slog.String("store_id", res.StoreID.String()))
					continue
				}
			}
			if trig == TriggerScoreDrop && diff != nil {
				if diff.ScoreDelta > -s.ScoreDropThreshold {
					// not enough drop for this subscription's threshold
					continue
				}
			}
			if err := d.send(ctx, res, diff, s, trig); err != nil {
				d.Logger.Warn("alerts_send_err",
					slog.String("trigger", trig),
					slog.String("subscription_id", s.ID.String()),
					slog.Any("err", err))
				continue
			}
			d.Logger.Info("alert_sent",
				slog.String("trigger", trig),
				slog.String("user_id", s.UserID.String()),
				slog.String("store_id", res.StoreID.String()),
				slog.String("audit_id", res.AuditID.String()),
				slog.String("target", s.Target))
		}
	}
}

// fired returns the list of trigger names that match this audit + diff.
func (d *Dispatcher) fired(res AuditResult, diff *differ.Diff) []string {
	var out []string
	if res.Status == "failed" {
		// only audit_failed fires for failed audits — score/diff are stale
		return []string{TriggerAuditFailed}
	}
	if diff == nil {
		return nil
	}
	if diff.NewCriticalCount > 0 {
		out = append(out, TriggerNewCritical)
	}
	// score drop only fires when we have a previous audit to compare to
	if diff.PrevScore != nil && diff.ScoreDelta <= -1 {
		out = append(out, TriggerScoreDrop)
	}
	return out
}

type subscription struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	UserEmail          string
	UserName           string
	Target             string
	OnNewCritical      bool
	OnScoreDrop        bool
	OnAuditFailed      bool
	OnGMCChange        bool
	ScoreDropThreshold int
}

func (s subscription) subscribesTo(trigger string) bool {
	switch trigger {
	case TriggerNewCritical:
		return s.OnNewCritical
	case TriggerScoreDrop:
		return s.OnScoreDrop
	case TriggerAuditFailed:
		return s.OnAuditFailed
	case TriggerGMCChange:
		return s.OnGMCChange
	}
	return false
}

func (d *Dispatcher) loadSubscriptions(ctx context.Context, tenantID, storeID uuid.UUID) ([]subscription, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT s.id, s.user_id, u.email, COALESCE(u.name, ''), s.target,
		       s.on_new_critical, s.on_score_drop, s.on_audit_failed, s.on_gmc_account_change,
		       s.score_drop_threshold
		FROM store_alert_subscriptions s
		JOIN users u ON u.id = s.user_id
		WHERE s.tenant_id = $1
		  AND (s.store_id = $2 OR s.store_id IS NULL)
		  AND s.enabled
		  AND s.channel = 'email'
	`, tenantID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subscription
	for rows.Next() {
		var s subscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.UserEmail, &s.UserName, &s.Target,
			&s.OnNewCritical, &s.OnScoreDrop, &s.OnAuditFailed, &s.OnGMCChange,
			&s.ScoreDropThreshold); err != nil {
			return nil, err
		}
		if s.Target == "" {
			s.Target = s.UserEmail
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// recentlyAlerted returns true if this user got any (rate-limited) alert
// for this store in the last 24h. always-send triggers (audit_failed,
// new_critical) don't count toward the limit but they also don't read it.
func (d *Dispatcher) recentlyAlerted(ctx context.Context, userID, storeID uuid.UUID) (bool, error) {
	var n int
	err := d.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM alert_dispatches
		WHERE user_id = $1
		  AND store_id = $2
		  AND trigger NOT IN ('audit_failed','new_critical')
		  AND sent_at > now() - interval '24 hours'
	`, userID, storeID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (d *Dispatcher) send(ctx context.Context, res AuditResult, diff *differ.Diff, s subscription, trig string) error {
	body, subject, err := d.render(res, diff, s, trig)
	if err != nil {
		return err
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Insert log row first; if it conflicts (already sent for this audit/user/trigger)
	// we abort cleanly without sending the email a second time.
	_, err = tx.Exec(ctx, `
		INSERT INTO alert_dispatches
		  (tenant_id, user_id, store_id, audit_id, subscription_id, trigger, channel, target)
		VALUES
		  ($1, $2, $3, $4, $5, $6, 'email', $7)
		ON CONFLICT (audit_id, user_id, trigger) WHERE user_id IS NOT NULL DO NOTHING
	`, res.TenantID, s.UserID, res.StoreID, res.AuditID, s.ID, trig, s.Target)
	if err != nil {
		return fmt.Errorf("insert dispatch log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return d.Mailer.Send(ctx, mailer.Compose(s.Target, d.MailFrom, subject, body))
}

func (d *Dispatcher) render(res AuditResult, diff *differ.Diff, s subscription, trig string) (body, subject string, err error) {
	reportURL := fmt.Sprintf("%s/audits/%s", d.BaseURL, res.AuditID.String())
	unsub := d.unsubscribeURL(s.ID, s.UserID, trig)

	data := mailer.AuditAlertData{
		Trigger:        trig,
		StoreName:      res.StoreName,
		ReportURL:      reportURL,
		UnsubscribeURL: unsub,
		Audit: mailer.AuditAlertAudit{
			Status:    res.Status,
			Score:     res.Score,
			RiskLevel: res.RiskLevel,
			Error:     res.ErrorMsg,
		},
	}
	if diff != nil {
		data.NewCount = diff.NewCount
		data.ResolvedCount = diff.ResolvedCount
		data.NewCriticalCount = diff.NewCriticalCount
		if diff.PrevScore != nil {
			data.PrevScore = *diff.PrevScore
			data.HavePrevScore = true
		}
		data.ScoreDelta = diff.ScoreDelta
		// top 3 new issues, severity-sorted (we already have them in order
		// from the differ; just take the first 3).
		max := 3
		if len(diff.NewIssues) < max {
			max = len(diff.NewIssues)
		}
		for _, ni := range diff.NewIssues[:max] {
			data.TopNewIssues = append(data.TopNewIssues, mailer.AuditAlertIssue{
				Severity:     ni.Severity,
				Title:        ni.Title,
				ProductTitle: ni.ProductTitle,
				PageURL:      ni.PageURL,
			})
		}
	}
	switch trig {
	case TriggerNewCritical:
		subject = fmt.Sprintf("New critical issue on %s", res.StoreName)
	case TriggerScoreDrop:
		subject = fmt.Sprintf("Audit score dropped on %s", res.StoreName)
	case TriggerAuditFailed:
		subject = fmt.Sprintf("Audit failed on %s", res.StoreName)
	default:
		subject = fmt.Sprintf("Update on %s", res.StoreName)
	}
	body, err = mailer.RenderAuditAlert(data)
	return
}

// unsubscribeURL signs (sub_id|trigger|user_id) so a click on the link
// proves the recipient owns the subscription without requiring login.
func (d *Dispatcher) unsubscribeURL(subID, userID uuid.UUID, trigger string) string {
	tok := SignUnsubscribe(d.AppSecret, subID, userID, trigger)
	return fmt.Sprintf("%s/unsubscribe/%s", d.BaseURL, tok)
}

// SignUnsubscribe / VerifyUnsubscribe live in this package so the web
// handlers and the dispatcher use exactly the same token format.

type unsubPayload struct {
	S string `json:"s"` // subscription id
	U string `json:"u"` // user id
	T string `json:"t"` // trigger name
}

func SignUnsubscribe(secret []byte, subID, userID uuid.UUID, trigger string) string {
	p := unsubPayload{S: subID.String(), U: userID.String(), T: trigger}
	body, _ := json.Marshal(p)
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte(bodyB64))
	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return bodyB64 + "." + sig
}

func VerifyUnsubscribe(secret []byte, token string) (subID, userID uuid.UUID, trigger string, err error) {
	for i, c := range token {
		if c == '.' {
			body := token[:i]
			sig := token[i+1:]
			h := hmac.New(sha256.New, secret)
			_, _ = h.Write([]byte(body))
			want := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(want)) {
				return uuid.Nil, uuid.Nil, "", errors.New("unsubscribe: bad signature")
			}
			raw, err2 := base64.RawURLEncoding.DecodeString(body)
			if err2 != nil {
				return uuid.Nil, uuid.Nil, "", err2
			}
			var p unsubPayload
			if err2 := json.Unmarshal(raw, &p); err2 != nil {
				return uuid.Nil, uuid.Nil, "", err2
			}
			subID, _ = uuid.Parse(p.S)
			userID, _ = uuid.Parse(p.U)
			return subID, userID, p.T, nil
		}
	}
	return uuid.Nil, uuid.Nil, "", errors.New("unsubscribe: malformed token")
}

// ApplyUnsubscribe flips the relevant on_* flag on the subscription. Caller
// must have already verified the token.
func ApplyUnsubscribe(ctx context.Context, tx pgx.Tx, subID, userID uuid.UUID, trigger string) error {
	col := ""
	switch trigger {
	case TriggerNewCritical:
		col = "on_new_critical"
	case TriggerScoreDrop:
		col = "on_score_drop"
	case TriggerAuditFailed:
		col = "on_audit_failed"
	case TriggerGMCChange:
		col = "on_gmc_account_change"
	default:
		return fmt.Errorf("unsubscribe: unknown trigger %q", trigger)
	}
	_, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE store_alert_subscriptions SET %s = false, updated_at = now() WHERE id = $1 AND user_id = $2`, col),
		subID, userID)
	return err
}
