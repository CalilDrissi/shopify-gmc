package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	a := &Audit{StoreID: st.ID, Trigger: "manual"}
	if err := (AuditsRepo{}).Insert(ctx, tx, tn.ID, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if a.Status != "queued" {
		t.Errorf("status=%q want queued", a.Status)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := (AuditsRepo{}).MarkRunning(ctx, tx, tn.ID, a.ID, now); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}

	t.Run("Finish table-driven", func(t *testing.T) {
		cases := []struct {
			name      string
			endStatus string
		}{
			{"success", "succeeded"},
			{"failure", "failed"},
			{"canceled", "canceled"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				a := &Audit{StoreID: st.ID, Trigger: "scheduled"}
				if err := (AuditsRepo{}).Insert(ctx, tx, tn.ID, a); err != nil {
					t.Fatalf("seed: %v", err)
				}
				_ = (AuditsRepo{}).MarkRunning(ctx, tx, tn.ID, a.ID, now)
				err := (AuditsRepo{}).Finish(ctx, tx, tn.ID, a.ID, tc.endStatus, now, 42, []byte(`{"critical":3}`), nil)
				if err != nil {
					t.Fatalf("Finish: %v", err)
				}
				got, _ := (AuditsRepo{}).GetByID(ctx, tx, tn.ID, a.ID)
				if got.Status != tc.endStatus {
					t.Errorf("status=%q want %q", got.Status, tc.endStatus)
				}
			})
		}
	})

	t.Run("ListByStore", func(t *testing.T) {
		list, err := (AuditsRepo{}).ListByStore(ctx, tx, tn.ID, st.ID, 10)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) < 4 {
			t.Errorf("got %d audits, want >=4", len(list))
		}
	})
}

func TestIssuesRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)
	a := &Audit{StoreID: st.ID, Trigger: "manual"}
	if err := (AuditsRepo{}).Insert(ctx, tx, tn.ID, a); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	cases := []struct {
		name, severity, rule, title string
	}{
		{"missing-gtin", "error", "GTIN_MISSING", "Missing GTIN"},
		{"low-image", "warning", "IMAGE_LOW_RES", "Image resolution too low"},
		{"price-mismatch", "critical", "PRICE_MISMATCH", "Price differs from feed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := &Issue{
				AuditID:  a.ID,
				StoreID:  st.ID,
				RuleCode: tc.rule,
				Severity: tc.severity,
				Title:    tc.title,
			}
			if err := (IssuesRepo{}).Insert(ctx, tx, tn.ID, i); err != nil {
				t.Fatalf("Insert: %v", err)
			}
		})
	}

	list, err := (IssuesRepo{}).ListByAudit(ctx, tx, tn.ID, a.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("got %d issues, want 3", len(list))
	}

	if err := (IssuesRepo{}).MarkStatus(ctx, tx, tn.ID, list[0].ID, "fixed", &time.Time{}); err != nil {
		t.Fatalf("MarkStatus: %v", err)
	}

	if err := (IssuesRepo{}).MarkStatus(ctx, tx, uuid.New(), list[0].ID, "ignored", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant MarkStatus: got %v want ErrNotFound", err)
	}
}

func TestAuditDiffsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	prev := &Audit{StoreID: st.ID, Trigger: "scheduled"}
	(AuditsRepo{}).Insert(ctx, tx, tn.ID, prev)
	curr := &Audit{StoreID: st.ID, Trigger: "scheduled"}
	(AuditsRepo{}).Insert(ctx, tx, tn.ID, curr)

	d := &AuditDiff{
		AuditID:            curr.ID,
		PreviousAuditID:    &prev.ID,
		NewIssueCount:      4,
		ResolvedIssueCount: 2,
		Diff:               []byte(`{"new":[],"resolved":[]}`),
	}
	if err := (AuditDiffsRepo{}).Insert(ctx, tx, tn.ID, d); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := (AuditDiffsRepo{}).GetByAudit(ctx, tx, tn.ID, curr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.NewIssueCount != 4 || got.ResolvedIssueCount != 2 {
		t.Errorf("counts wrong: %+v", got)
	}
}

func TestAuditJobsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	now := time.Now().UTC()

	cases := []struct {
		name, kind string
	}{
		{"shopify-sync", "sync_shopify"},
		{"gmc-snapshot", "snapshot_gmc"},
		{"audit-store", "audit_store"},
	}
	var ids []uuid.UUID
	for _, tc := range cases {
		t.Run("enqueue-"+tc.name, func(t *testing.T) {
			j, err := (AuditJobsRepo{}).Enqueue(ctx, tx, &tn.ID, tc.kind, []byte(`{"x":1}`), now)
			if err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
			if j.Status != "queued" {
				t.Errorf("status=%q want queued", j.Status)
			}
			ids = append(ids, j.ID)
		})
	}

	t.Run("ClaimNext", func(t *testing.T) {
		j, err := (AuditJobsRepo{}).ClaimNext(ctx, tx, "worker-1", now.Add(time.Second))
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if j.Status != "running" {
			t.Errorf("status=%q want running", j.Status)
		}
		if j.Attempts != 1 {
			t.Errorf("attempts=%d want 1", j.Attempts)
		}
		if err := (AuditJobsRepo{}).MarkSucceeded(ctx, tx, j.ID, now.Add(2*time.Second)); err != nil {
			t.Fatalf("MarkSucceeded: %v", err)
		}
	})

	t.Run("MarkFailed retry", func(t *testing.T) {
		j, _ := (AuditJobsRepo{}).ClaimNext(ctx, tx, "worker-1", now.Add(time.Second))
		retry := now.Add(time.Minute)
		if err := (AuditJobsRepo{}).MarkFailed(ctx, tx, j.ID, "transient", &retry); err != nil {
			t.Fatalf("MarkFailed retry: %v", err)
		}
	})

	t.Run("MarkFailed dead", func(t *testing.T) {
		j, _ := (AuditJobsRepo{}).ClaimNext(ctx, tx, "worker-1", now.Add(time.Hour))
		if j == nil {
			t.Skip("no job to fail")
		}
		if err := (AuditJobsRepo{}).MarkFailed(ctx, tx, j.ID, "permanent", nil); err != nil {
			t.Fatalf("MarkFailed dead: %v", err)
		}
	})
}
