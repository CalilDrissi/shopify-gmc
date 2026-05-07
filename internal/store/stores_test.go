package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStoresRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)

	s := seedStore(ctx, t, tx, tn.ID)
	t.Run("Insert+GetByID", func(t *testing.T) {
		got, err := (StoresRepo{}).GetByID(ctx, tx, tn.ID, s.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Status != "connected" {
			t.Errorf("status=%q want connected", got.Status)
		}
		if !got.MonitorEnabled {
			t.Error("monitor_enabled should default true")
		}
	})

	t.Run("Tenant isolation", func(t *testing.T) {
		other := seedTenant(ctx, t, tx)
		_, err := (StoresRepo{}).GetByID(ctx, tx, other.ID, s.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("cross-tenant GetByID: got %v want ErrNotFound", err)
		}
	})

	t.Run("UpdateStatus table-driven", func(t *testing.T) {
		cases := []struct {
			name, status string
		}{
			{"to-disconnected", "disconnected"},
			{"to-error", "error"},
			{"to-paused", "paused"},
			{"back-to-connected", "connected"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if err := (StoresRepo{}).UpdateStatus(ctx, tx, tn.ID, s.ID, tc.status); err != nil {
					t.Fatalf("Update: %v", err)
				}
				got, _ := (StoresRepo{}).GetByID(ctx, tx, tn.ID, s.ID)
				if got.Status != tc.status {
					t.Errorf("got %q want %q", got.Status, tc.status)
				}
			})
		}
	})

	t.Run("UpdateMonitorSettings", func(t *testing.T) {
		err := (StoresRepo{}).UpdateMonitorSettings(ctx, tx, tn.ID, s.ID, false, 12*time.Hour, "critical")
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, _ := (StoresRepo{}).GetByID(ctx, tx, tn.ID, s.ID)
		if got.MonitorEnabled {
			t.Error("monitor should be off")
		}
		if got.MonitorAlertThreshold != "critical" {
			t.Errorf("threshold=%q want critical", got.MonitorAlertThreshold)
		}
	})

	t.Run("ListByTenant", func(t *testing.T) {
		_ = seedStore(ctx, t, tx, tn.ID)
		_ = seedStore(ctx, t, tx, tn.ID)
		list, err := (StoresRepo{}).ListByTenant(ctx, tx, tn.ID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("got %d stores, want 3", len(list))
		}
	})
}

func TestStoreAlertSubscriptionsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	cases := []struct {
		name, channel, target string
	}{
		{"email", "email", "ops@example.com"},
		{"webhook", "webhook", "https://hooks.example.com/x"},
		{"slack", "slack", "https://hooks.slack.com/services/AAA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := &StoreAlertSubscription{
				StoreID: &st.ID,
				Channel: tc.channel,
				Target:  tc.target,
			}
			if err := (StoreAlertSubscriptionsRepo{}).Insert(ctx, tx, tn.ID, sub); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if !sub.Enabled {
				t.Error("enabled should default true")
			}
		})
	}

	list, err := (StoreAlertSubscriptionsRepo{}).ListByTenant(ctx, tx, tn.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len=%d want 3", len(list))
	}

	if err := (StoreAlertSubscriptionsRepo{}).SetEnabled(ctx, tx, tn.ID, list[0].ID, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	otherTenant := uuid.New()
	if err := (StoreAlertSubscriptionsRepo{}).SetEnabled(ctx, tx, otherTenant, list[0].ID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant SetEnabled: got %v want ErrNotFound", err)
	}
}

func TestStoreGmcConnectionsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	st := seedStore(ctx, t, tx, tn.ID)

	c := &StoreGmcConnection{
		StoreID:    st.ID,
		MerchantID: "MC-12345",
	}
	if err := (StoreGmcConnectionsRepo{}).Insert(ctx, tx, tn.ID, c); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if c.Status != "active" {
		t.Errorf("status=%q want active", c.Status)
	}

	got, err := (StoreGmcConnectionsRepo{}).GetByStore(ctx, tx, tn.ID, st.ID)
	if err != nil {
		t.Fatalf("GetByStore: %v", err)
	}
	if got.MerchantID != "MC-12345" {
		t.Errorf("merchant_id mismatch")
	}

	now := time.Now().Add(time.Hour).UTC()
	if err := (StoreGmcConnectionsRepo{}).UpdateTokens(ctx, tx, tn.ID, c.ID,
		[]byte("access"), []byte("refresh"), []byte("nonce"), &now); err != nil {
		t.Fatalf("UpdateTokens: %v", err)
	}

	if err := (StoreGmcConnectionsRepo{}).MarkRevoked(ctx, tx, tn.ID, c.ID); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	got, _ = (StoreGmcConnectionsRepo{}).GetByStore(ctx, tx, tn.ID, st.ID)
	if got.Status != "revoked" {
		t.Errorf("status=%q want revoked", got.Status)
	}
}
