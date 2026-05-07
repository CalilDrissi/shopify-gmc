package store

import (
	"testing"
	"time"
)

func TestPurchasesRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	u := seedUser(ctx, t, tx)

	cases := []struct {
		name, plan, license string
	}{
		{"pro-yearly", "pro", "LIC-AAA-" + randSuffix(t)},
		{"agency-monthly", "agency", "LIC-BBB-" + randSuffix(t)},
		{"enterprise", "enterprise", "LIC-CCC-" + randSuffix(t)},
	}
	for _, tc := range cases {
		t.Run("insert-"+tc.name, func(t *testing.T) {
			p := &Purchase{
				TenantID:   &tn.ID,
				UserID:     &u.ID,
				LicenseKey: &tc.license,
				Plan:       tc.plan,
			}
			if err := (PurchasesRepo{}).Insert(ctx, tx, p); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if p.Status != "active" {
				t.Errorf("status=%q want active", p.Status)
			}
		})
	}

	t.Run("GetByLicense", func(t *testing.T) {
		got, err := (PurchasesRepo{}).GetByLicense(ctx, tx, cases[0].license)
		if err != nil {
			t.Fatalf("GetByLicense: %v", err)
		}
		if got.Plan != "pro" {
			t.Errorf("plan=%q want pro", got.Plan)
		}
	})

	t.Run("ListByTenant", func(t *testing.T) {
		list, err := (PurchasesRepo{}).ListByTenant(ctx, tx, tn.ID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("len=%d want 3", len(list))
		}
	})

	t.Run("MarkRefunded", func(t *testing.T) {
		got, _ := (PurchasesRepo{}).GetByLicense(ctx, tx, cases[0].license)
		if err := (PurchasesRepo{}).MarkRefunded(ctx, tx, got.ID, time.Now().UTC()); err != nil {
			t.Fatalf("MarkRefunded: %v", err)
		}
		got2, _ := (PurchasesRepo{}).GetByLicense(ctx, tx, cases[0].license)
		if got2.Status != "refunded" {
			t.Errorf("status=%q want refunded", got2.Status)
		}
	})
}

func TestGumroadWebhookEventsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)

	cases := []struct {
		name, event string
	}{
		{"sale", "sale"},
		{"refund", "refund"},
		{"cancellation", "cancellation"},
	}
	var ids []string
	for _, tc := range cases {
		t.Run("insert-"+tc.name, func(t *testing.T) {
			eid := tc.event + "-" + randSuffix(t)
			e := &GumroadWebhookEvent{
				GumroadEventID: &eid,
				EventType:      tc.event,
				Payload:        []byte(`{"id":"x"}`),
			}
			if err := (GumroadWebhookEventsRepo{}).Insert(ctx, tx, e); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			ids = append(ids, eid)
		})
	}

	list, err := (GumroadWebhookEventsRepo{}).ListUnprocessed(ctx, tx, 10)
	if err != nil {
		t.Fatalf("ListUnprocessed: %v", err)
	}
	if len(list) < 3 {
		t.Errorf("len=%d want >=3", len(list))
	}

	if err := (GumroadWebhookEventsRepo{}).MarkProcessed(ctx, tx, list[0].ID, time.Now().UTC()); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if err := (GumroadWebhookEventsRepo{}).MarkFailed(ctx, tx, list[1].ID, "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
}
