package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/audit/differ"
)

// PgPersister writes audit pipeline output to the audits + issues tables.
// The audit row is created by the enqueue handler at status='queued'; the
// pipeline UPDATEs it through running → succeeded/failed.
type PgPersister struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// AfterCommit, if set, fires once the audit transaction commits with
	// status='succeeded'. Used by the worker to trigger alerts. The diff is
	// passed in so the dispatcher doesn't have to re-load it.
	AfterCommit func(ctx context.Context, in AuditInput, out *AuditOutput, diff *differ.Diff)
}

func (p *PgPersister) Save(ctx context.Context, in AuditInput, out *AuditOutput) error {
	if p == nil || p.Pool == nil {
		return nil
	}
	progressBytes, _ := json.Marshal(out.Stages)
	stepsBytes, _ := json.Marshal(out.NextSteps)
	countsBytes, _ := json.Marshal(out.Counts)

	productCount := 0
	for _, r := range out.Results {
		if r.Meta.ID == "broken_product_links" {
			// the broken-link check sees one issue per failed page; not a
			// product count. Use the per-result Issues list instead.
			break
		}
	}

	status := "succeeded"
	for _, s := range out.Stages {
		if s.Status == "failed" {
			status = "failed"
			break
		}
	}

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("persist begin: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE audits
		SET status         = $2::audit_status,
		    started_at     = COALESCE(started_at, $3),
		    finished_at    = $3,
		    product_count  = $4,
		    issue_counts   = $5,
		    progress       = $6,
		    score          = $7,
		    risk_level     = $8,
		    summary        = $9,
		    next_steps     = $10,
		    error_message  = NULLIF($11,''),
		    updated_at     = now()
		WHERE id = $1
	`, in.AuditID, status, time.Now().UTC(), productCount, countsBytes,
		progressBytes, out.Score, out.RiskLevel, out.Summary, stepsBytes,
		errorMessage(out))
	if err != nil {
		return fmt.Errorf("persist update audit: %w", err)
	}

	// Replace any prior issues for this audit so re-running the persistence
	// stage stays idempotent.
	if _, err := tx.Exec(ctx, `DELETE FROM issues WHERE audit_id = $1`, in.AuditID); err != nil {
		return fmt.Errorf("persist clear issues: %w", err)
	}

	for _, r := range out.Results {
		if r.Status != StatusFail && r.Status != StatusInfo {
			continue
		}
		for i, iss := range r.Issues {
			key := fmt.Sprintf("%s#%d", r.Meta.ID, i)
			suggestion := out.Suggestions[key]
			payload, _ := json.Marshal(map[string]any{
				"meta":         r.Meta,
				"instructions": fetchInstructions(r.Meta.ID),
				"why":          fetchInstructions(r.Meta.ID).WhyItMatters,
				"docs_url":     fetchInstructions(r.Meta.ID).DocsURL,
				"category":     r.Meta.Category,
			})
			productID := iss.URL
			productTitle := iss.ProductTitle
			source := r.Meta.Source
			if source == "" {
				source = "crawler"
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO issues
				  (tenant_id, audit_id, store_id,
				   product_id, product_title, rule_code,
				   severity, status, title, description,
				   fix_instructions, fix_payload,
				   source, external_issue_code)
				VALUES
				  ($1, $2, $3,
				   NULLIF($4,''), NULLIF($5,''), $6,
				   $7::issue_severity, $8::issue_status, $9, NULLIF($10,''),
				   NULLIF($11,''), $12,
				   $13, NULLIF($14,''))
			`, in.TenantID, in.AuditID, in.StoreID,
				productID, productTitle, r.Meta.ID,
				string(r.Severity), "open", r.Meta.Title, iss.Detail,
				suggestion, payload,
				source, iss.ExternalCode)
			if err != nil {
				return fmt.Errorf("persist issue %s: %w", key, err)
			}
		}
	}

	// Compute and persist the audit_diffs row inside the same tx, so the
	// "before/after" picture is atomic with the issue inserts above. This
	// runs only on succeeded audits — failed audits don't carry meaningful
	// issue data to compare against.
	var diff *differ.Diff
	if status == "succeeded" {
		d, err := differ.Persist(ctx, tx, in.TenantID, in.StoreID, in.AuditID, out.Score)
		if err != nil {
			return fmt.Errorf("persist diff: %w", err)
		}
		diff = d
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if p.AfterCommit != nil {
		p.AfterCommit(ctx, in, out, diff)
	}
	return nil
}

// SaveProgress is called between stages so the live HTMX page can poll for an
// up-to-date stage list while the audit is still running.
//
// The status arg is honoured only for the queued→running transition. Once
// the row is at running/succeeded/failed, SaveProgress leaves status alone —
// otherwise the deferred flushProgress on the persist stage would clobber the
// final 'succeeded' status that Save just wrote.
func (p *PgPersister) SaveProgress(ctx context.Context, auditID string, stages []StageResult, _ string) error {
	if p == nil || p.Pool == nil {
		return nil
	}
	progressBytes, _ := json.Marshal(stages)
	_, err := p.Pool.Exec(ctx, `
		UPDATE audits
		SET status = CASE WHEN status = 'queued' THEN 'running'::audit_status ELSE status END,
		    started_at = COALESCE(started_at, now()),
		    progress = $2,
		    updated_at = now()
		WHERE id = $1::uuid
	`, auditID, progressBytes)
	return err
}

func errorMessage(out *AuditOutput) string {
	for _, s := range out.Stages {
		if s.Status == "failed" {
			return s.Name + ": " + s.Detail
		}
	}
	return ""
}

// fetchInstructions resolves the registered Check by ID and returns its
// hand-written FixInstructions, or a zero value if not registered.
func fetchInstructions(checkID string) FixInstructions {
	c, ok := Get(checkID)
	if !ok || c.Instructions == nil {
		return FixInstructions{}
	}
	return c.Instructions()
}
