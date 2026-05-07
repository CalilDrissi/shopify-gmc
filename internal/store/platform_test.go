package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPlatformAdminsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	u := seedUser(ctx, t, tx)

	t.Run("not admin initially", func(t *testing.T) {
		ok, err := (PlatformAdminsRepo{}).IsPlatformAdmin(ctx, tx, u.ID)
		if err != nil {
			t.Fatalf("IsPlatformAdmin: %v", err)
		}
		if ok {
			t.Error("user should not be admin")
		}
	})

	cases := []struct {
		name, role string
	}{
		{"as-support", "support"},
		{"upgrade-to-admin", "admin"},
		{"upgrade-to-super", "super"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, err := (PlatformAdminsRepo{}).Grant(ctx, tx, u.ID, tc.role)
			if err != nil {
				t.Fatalf("Grant: %v", err)
			}
			if a.Role != tc.role {
				t.Errorf("role=%q want %q", a.Role, tc.role)
			}
		})
	}

	t.Run("IsPlatformAdmin after grant", func(t *testing.T) {
		ok, err := (PlatformAdminsRepo{}).IsPlatformAdmin(ctx, tx, u.ID)
		if err != nil {
			t.Fatalf("IsPlatformAdmin: %v", err)
		}
		if !ok {
			t.Error("user should be admin")
		}
	})

	t.Run("Revoke", func(t *testing.T) {
		if err := (PlatformAdminsRepo{}).Revoke(ctx, tx, u.ID); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	})
}

func TestPlatformAdminAuditLogRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	u := seedUser(ctx, t, tx)
	ip := "10.0.0.1"
	target := "tenant"
	id := "tenant-id-123"

	cases := []struct {
		name, action string
	}{
		{"impersonate", "impersonate_user"},
		{"refund", "issue_refund"},
		{"reset-pw", "force_password_reset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &PlatformAdminAuditLogEntry{
				AdminUserID: &u.ID,
				Action:      tc.action,
				TargetType:  &target,
				TargetID:    &id,
				Metadata:    []byte(`{"reason":"customer-request"}`),
				IPAddress:   &ip,
			}
			if err := (PlatformAdminAuditLogRepo{}).Insert(ctx, tx, e); err != nil {
				t.Fatalf("Insert: %v", err)
			}
		})
	}

	list, err := (PlatformAdminAuditLogRepo{}).ListByAdmin(ctx, tx, u.ID, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len=%d want 3", len(list))
	}
}

func TestImpersonationLogRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	admin := seedUser(ctx, t, tx)
	target := seedUser(ctx, t, tx)
	tn := seedTenant(ctx, t, tx)

	reason := "support ticket #99"
	e := &ImpersonationLogEntry{
		AdminUserID:        &admin.ID,
		ImpersonatedUserID: &target.ID,
		TenantID:           &tn.ID,
		Reason:             &reason,
	}
	if err := (ImpersonationLogRepo{}).Start(ctx, tx, e); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := (ImpersonationLogRepo{}).End(ctx, tx, e.ID, time.Now().UTC()); err != nil {
		t.Fatalf("End: %v", err)
	}

	list, err := (ImpersonationLogRepo{}).ListByAdmin(ctx, tx, admin.ID, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("len=%d want 1", len(list))
	}
	if list[0].EndedAt == nil {
		t.Error("EndedAt should be set")
	}
}

func TestPlatformSettingsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	key := "feature.audit_v2"

	cases := []struct {
		name, value string
	}{
		{"set-bool", `true`},
		{"set-string", `"alpha"`},
		{"set-object", `{"enabled":true,"rollout":0.25}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (PlatformSettingsRepo{}).Set(ctx, tx, key, []byte(tc.value)); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := (PlatformSettingsRepo{}).Get(ctx, tx, key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			var gotV, wantV any
			if err := json.Unmarshal(got.Value, &gotV); err != nil {
				t.Fatalf("decode got: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.value), &wantV); err != nil {
				t.Fatalf("decode want: %v", err)
			}
			if !reflect.DeepEqual(gotV, wantV) {
				t.Errorf("value=%v want %v", gotV, wantV)
			}
		})
	}

	t.Run("Delete", func(t *testing.T) {
		if err := (PlatformSettingsRepo{}).Delete(ctx, tx, key); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := (PlatformSettingsRepo{}).Get(ctx, tx, key)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})
}
