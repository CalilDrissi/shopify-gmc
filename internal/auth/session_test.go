package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionStore_CreateGet(t *testing.T) {
	t.Parallel()
	db := NewMemSessionDB()
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	store := NewSessionStore(db, time.Hour).WithClock(func() time.Time { return now })

	user := uuid.New()
	sess, err := store.Create(context.Background(), user, "10.0.0.1", "ua")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sess.Token == "" {
		t.Error("token empty")
	}
	if sess.UserID != user {
		t.Errorf("user mismatch: got %v want %v", sess.UserID, user)
	}
	if !sess.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("expires_at=%v want %v", sess.ExpiresAt, now.Add(time.Hour))
	}

	got, err := store.Get(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("id mismatch: %v vs %v", got.ID, sess.ID)
	}

	_, err = store.Get(context.Background(), "garbage")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestSessionStore_Extend(t *testing.T) {
	t.Parallel()
	db := NewMemSessionDB()
	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	clk := t0
	store := NewSessionStore(db, time.Hour).WithClock(func() time.Time { return clk })

	sess, _ := store.Create(context.Background(), uuid.New(), "", "")
	clk = t0.Add(45 * time.Minute)
	if err := store.Extend(context.Background(), sess.ID); err != nil {
		t.Fatalf("extend: %v", err)
	}

	got, err := store.Get(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := clk.Add(time.Hour)
	if !got.ExpiresAt.Equal(want) {
		t.Errorf("expires_at=%v want %v", got.ExpiresAt, want)
	}
}

func TestSessionStore_Revoke(t *testing.T) {
	t.Parallel()
	db := NewMemSessionDB()
	store := NewSessionStore(db, time.Hour)

	sess, _ := store.Create(context.Background(), uuid.New(), "", "")
	if err := store.Revoke(context.Background(), sess.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := store.Get(context.Background(), sess.Token)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("after revoke want ErrSessionNotFound, got %v", err)
	}
}

func TestSessionStore_Expired(t *testing.T) {
	t.Parallel()
	db := NewMemSessionDB()
	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	clk := t0
	store := NewSessionStore(db, time.Hour).WithClock(func() time.Time { return clk })

	sess, _ := store.Create(context.Background(), uuid.New(), "", "")
	clk = t0.Add(2 * time.Hour)

	_, err := store.Get(context.Background(), sess.Token)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("after expiry want ErrSessionNotFound, got %v", err)
	}
}
