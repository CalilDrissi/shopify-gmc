// Package differ computes the diff between two audits for the same store.
//
// An "issue identity" is the tuple (check_id, page_url) — same rule firing
// on the same page is the same issue across runs. The differ classifies
// every current+previous issue into:
//
//   - new       (in current but not in previous)
//   - resolved  (in previous but not in current)
//   - unchanged (in both)
//
// The differ is called inside the same transaction that the audit's persister
// commits, so the audit_diffs row is guaranteed to exist for every successful
// audit (or be absent if the audit failed, since the tx rolls back).
package differ

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IssueKey is the identity tuple. Severity/title are carried so the alert
// dispatcher can format an email without re-querying.
type IssueKey struct {
	CheckID      string `json:"check_id"`
	PageURL      string `json:"page_url"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	ProductTitle string `json:"product_title,omitempty"`
}

func (k IssueKey) id() string { return k.CheckID + "\x00" + k.PageURL }

// Diff is the structured output the differ writes to audit_diffs.
type Diff struct {
	NewCount         int        `json:"-"`
	ResolvedCount    int        `json:"-"`
	UnchangedCount   int        `json:"-"`
	NewCriticalCount int        `json:"-"`
	PrevScore        *int       `json:"-"`
	NewScore         int        `json:"-"`
	ScoreDelta       int        `json:"-"` // 0 if no previous audit
	NewIssues        []IssueKey `json:"new_issues"`
	ResolvedIssues   []IssueKey `json:"resolved_issues"`
}

// Compute classifies each issue. The previous slice may be nil for the
// first-ever audit on a store; in that case every current issue is "new".
func Compute(prev, curr []IssueKey, prevScore *int, newScore int) Diff {
	prevByKey := make(map[string]IssueKey, len(prev))
	for _, k := range prev {
		prevByKey[k.id()] = k
	}
	currByKey := make(map[string]IssueKey, len(curr))
	for _, k := range curr {
		currByKey[k.id()] = k
	}
	var d Diff
	d.PrevScore = prevScore
	d.NewScore = newScore
	if prevScore != nil {
		d.ScoreDelta = newScore - *prevScore
	}
	for _, k := range curr {
		if _, was := prevByKey[k.id()]; was {
			d.UnchangedCount++
			continue
		}
		d.NewCount++
		if k.Severity == "critical" {
			d.NewCriticalCount++
		}
		d.NewIssues = append(d.NewIssues, k)
	}
	for _, k := range prev {
		if _, still := currByKey[k.id()]; !still {
			d.ResolvedCount++
			d.ResolvedIssues = append(d.ResolvedIssues, k)
		}
	}
	return d
}

// LoadIssues fetches the issue rows for an audit as IssueKeys.
// Reads through the supplied tx so it sees uncommitted writes from the
// current audit's persistence stage.
func LoadIssues(ctx context.Context, tx pgx.Tx, auditID uuid.UUID) ([]IssueKey, error) {
	rows, err := tx.Query(ctx, `
		SELECT rule_code, COALESCE(product_id, ''), severity::text, title, COALESCE(product_title, '')
		FROM issues
		WHERE audit_id = $1
	`, auditID)
	if err != nil {
		return nil, fmt.Errorf("differ: load issues: %w", err)
	}
	defer rows.Close()
	var keys []IssueKey
	for rows.Next() {
		var k IssueKey
		if err := rows.Scan(&k.CheckID, &k.PageURL, &k.Severity, &k.Title, &k.ProductTitle); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Persist looks up the most recent succeeded audit for the same store
// (excluding the current audit), classifies issues, and inserts an
// audit_diffs row.
//
// If no previous audit exists, the diff is "all current issues are new" and
// previous_audit_id is left NULL.
//
// Idempotent: if a row for this audit_id already exists, it is overwritten.
func Persist(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, storeID, auditID uuid.UUID,
	currScore int,
) (*Diff, error) {
	currIssues, err := LoadIssues(ctx, tx, auditID)
	if err != nil {
		return nil, err
	}

	var (
		prevID    uuid.UUID
		prevScore *int
	)
	row := tx.QueryRow(ctx, `
		SELECT id, score
		FROM audits
		WHERE store_id   = $1
		  AND tenant_id  = $2
		  AND status     = 'succeeded'
		  AND id         <> $3
		ORDER BY finished_at DESC NULLS LAST, created_at DESC
		LIMIT 1
	`, storeID, tenantID, auditID)
	if err := row.Scan(&prevID, &prevScore); err != nil {
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("differ: prev audit lookup: %w", err)
		}
		prevID = uuid.Nil
	}

	var prevIssues []IssueKey
	if prevID != uuid.Nil {
		prevIssues, err = LoadIssues(ctx, tx, prevID)
		if err != nil {
			return nil, err
		}
	}

	d := Compute(prevIssues, currIssues, prevScore, currScore)
	payload, _ := json.Marshal(d)

	var prevIDArg interface{}
	if prevID != uuid.Nil {
		prevIDArg = prevID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_diffs
		  (tenant_id, audit_id, previous_audit_id,
		   new_issue_count, resolved_issue_count, unchanged_count, new_critical_count,
		   prev_score, new_score, score_delta, diff)
		VALUES
		  ($1, $2, $3,
		   $4, $5, $6, $7,
		   $8, $9, $10, $11)
		ON CONFLICT (audit_id) DO UPDATE SET
		  previous_audit_id    = EXCLUDED.previous_audit_id,
		  new_issue_count      = EXCLUDED.new_issue_count,
		  resolved_issue_count = EXCLUDED.resolved_issue_count,
		  unchanged_count      = EXCLUDED.unchanged_count,
		  new_critical_count   = EXCLUDED.new_critical_count,
		  prev_score           = EXCLUDED.prev_score,
		  new_score            = EXCLUDED.new_score,
		  score_delta          = EXCLUDED.score_delta,
		  diff                 = EXCLUDED.diff
	`, tenantID, auditID, prevIDArg,
		d.NewCount, d.ResolvedCount, d.UnchangedCount, d.NewCriticalCount,
		prevScore, currScore, d.ScoreDelta, payload)
	if err != nil {
		return nil, fmt.Errorf("differ: insert audit_diff: %w", err)
	}
	return &d, nil
}
