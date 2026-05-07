package store

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTenantsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)

	t.Run("Insert+GetByID+GetBySlug", func(t *testing.T) {
		tn := seedTenant(ctx, t, tx)
		got, err := (TenantsRepo{}).GetByID(ctx, tx, tn.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Slug != tn.Slug {
			t.Errorf("slug mismatch")
		}
		got2, err := (TenantsRepo{}).GetBySlug(ctx, tx, tn.Slug)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if got2.ID != tn.ID {
			t.Errorf("id mismatch")
		}
	})

	t.Run("UpdatePlan table-driven", func(t *testing.T) {
		cases := []struct {
			name string
			plan string
		}{
			{"to-pro", "pro"},
			{"to-agency", "agency"},
			{"to-enterprise", "enterprise"},
			{"back-to-free", "free"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tn := seedTenant(ctx, t, tx)
				if err := (TenantsRepo{}).UpdatePlan(ctx, tx, tn.ID, tc.plan, nil); err != nil {
					t.Fatalf("UpdatePlan: %v", err)
				}
				got, _ := (TenantsRepo{}).GetByID(ctx, tx, tn.ID)
				if got.Plan != tc.plan {
					t.Errorf("plan=%q want %q", got.Plan, tc.plan)
				}
			})
		}
	})
}

func TestMembershipsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	owner := seedUser(ctx, t, tx)
	mem := seedUser(ctx, t, tx)

	if err := (MembershipsRepo{}).Insert(ctx, tx, tn.ID, &Membership{UserID: owner.ID, Role: "owner"}); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if err := (MembershipsRepo{}).Insert(ctx, tx, tn.ID, &Membership{UserID: mem.ID, Role: "member"}); err != nil {
		t.Fatalf("insert member: %v", err)
	}

	t.Run("ListByTenant", func(t *testing.T) {
		list, err := (MembershipsRepo{}).ListByTenant(ctx, tx, tn.ID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("got %d memberships, want 2", len(list))
		}
	})

	t.Run("Remove", func(t *testing.T) {
		if err := (MembershipsRepo{}).Remove(ctx, tx, tn.ID, mem.ID); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		_, err := (MembershipsRepo{}).GetByTenantAndUser(ctx, tx, tn.ID, mem.ID)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("after remove want ErrNotFound, got %v", err)
		}
	})
}

func TestMembershipsRepo_OneOwnerPerTenant(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	owner := seedUser(ctx, t, tx)
	extra := seedUser(ctx, t, tx)
	if err := (MembershipsRepo{}).Insert(ctx, tx, tn.ID, &Membership{UserID: owner.ID, Role: "owner"}); err != nil {
		t.Fatalf("first owner: %v", err)
	}
	err := (MembershipsRepo{}).Insert(ctx, tx, tn.ID, &Membership{UserID: extra.ID, Role: "owner"})
	if err == nil {
		t.Error("expected error from one-owner-per-tenant unique index")
	}
}

func TestInvitationsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)
	inviter := seedUser(ctx, t, tx)

	hash := sha256.Sum256([]byte("invite-tok"))
	inv := &Invitation{
		InviterID: &inviter.ID,
		Email:     "newbie-" + randSuffix(t) + "@example.com",
		Role:      "member",
		TokenHash: hash[:],
		ExpiresAt: time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := (InvitationsRepo{}).Insert(ctx, tx, tn.ID, inv); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := (InvitationsRepo{}).GetByTokenHash(ctx, tx, hash[:])
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status=%q want pending", got.Status)
	}

	t.Run("ListPendingByTenant", func(t *testing.T) {
		list, err := (InvitationsRepo{}).ListPendingByTenant(ctx, tx, tn.ID)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("got %d, want 1", len(list))
		}
	})

	t.Run("MarkAccepted", func(t *testing.T) {
		now := time.Now().UTC()
		if err := (InvitationsRepo{}).MarkAccepted(ctx, tx, tn.ID, inv.ID, now); err != nil {
			t.Fatalf("MarkAccepted: %v", err)
		}
		got, _ := (InvitationsRepo{}).GetByTokenHash(ctx, tx, hash[:])
		if got.Status != "accepted" {
			t.Errorf("status=%q want accepted", got.Status)
		}
	})

	t.Run("Tenant isolation: cannot accept another tenant's invitation", func(t *testing.T) {
		otherTenant := seedTenant(ctx, t, tx)
		err := (InvitationsRepo{}).MarkAccepted(ctx, tx, otherTenant.ID, inv.ID, time.Now().UTC())
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("cross-tenant want ErrNotFound, got %v", err)
		}
	})
}

func TestUsageCountersRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	tn := seedTenant(ctx, t, tx)

	periodStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)

	c1, err := (UsageCountersRepo{}).Increment(ctx, tx, tn.ID, "audits_run", periodStart, periodEnd, 1)
	if err != nil {
		t.Fatalf("Increment 1: %v", err)
	}
	if c1.Count != 1 {
		t.Errorf("count=%d want 1", c1.Count)
	}
	c2, _ := (UsageCountersRepo{}).Increment(ctx, tx, tn.ID, "audits_run", periodStart, periodEnd, 5)
	if c2.Count != 6 {
		t.Errorf("count=%d want 6", c2.Count)
	}

	got, err := (UsageCountersRepo{}).Get(ctx, tx, tn.ID, "audits_run", periodStart)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Count != 6 {
		t.Errorf("Get.count=%d want 6", got.Count)
	}

	if _, err := (UsageCountersRepo{}).Get(ctx, tx, uuid.New(), "audits_run", periodStart); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-tenant want ErrNotFound, got %v", err)
	}
}
