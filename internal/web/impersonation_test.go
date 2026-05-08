package web

// Impersonation guard test.
//
// The contract:
//   1. RequireOwner blocks destructive writes when ImpersonatingFromContext(r) is true.
//   2. Every impersonated session is recorded in impersonation_log (via the
//      admin handler that starts the session — covered by flow-admin.js
//      end-to-end).
//
// This test exercises the middleware in isolation: build a Request whose
// context has both a "membership=owner" entry AND the "impersonating" flag
// set, send it through RequireOwner-wrapped handler, and verify the
// downstream handler is NOT invoked.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/example/gmcauditor/internal/store"
)

func TestRequireOwner_BlocksImpersonatedDestructiveWrite(t *testing.T) {
	called := false
	downstream := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	render403 := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("blocked"))
	}
	wrapped := RequireOwner(render403)(downstream)

	req := httptest.NewRequest("POST", "/t/acme/stores/00000000-0000-0000-0000-000000000001/delete", nil)
	ctx := WithMembership(req.Context(), &store.Membership{
		UserID: uuid.New(),
		Role:   "owner",
	})
	ctx = WithImpersonation(ctx, true)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("got status %d; want 403", rr.Code)
	}
	if called {
		t.Error("downstream handler was invoked despite impersonation; the guard regressed")
	}
}

// TestRequireOwner_AllowsRealOwnerWrite is the positive control for the
// guard above — without the impersonation flag, the real owner sails
// through.
func TestRequireOwner_AllowsRealOwnerWrite(t *testing.T) {
	called := false
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	render403 := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	wrapped := RequireOwner(render403)(downstream)

	req := httptest.NewRequest("POST", "/t/acme/stores/00000000-0000-0000-0000-000000000001/delete", nil)
	ctx := WithMembership(req.Context(), &store.Membership{Role: "owner"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got status %d; want 200", rr.Code)
	}
	if !called {
		t.Error("downstream handler was NOT invoked for a real owner")
	}
}

// TestRequireOwner_BlocksMember confirms the role check itself.
func TestRequireOwner_BlocksMember(t *testing.T) {
	called := false
	downstream := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	render403 := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	wrapped := RequireOwner(render403)(downstream)

	req := httptest.NewRequest("POST", "/t/acme/stores/00000000-0000-0000-0000-000000000001/delete", nil)
	ctx := WithMembership(req.Context(), &store.Membership{Role: "member"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("got status %d; want 403", rr.Code)
	}
	if called {
		t.Error("downstream handler was invoked for a non-owner")
	}
}

// suppress "declared and not used" lint when context.Context isn't surfaced
// in failure paths.
var _ = context.Background
