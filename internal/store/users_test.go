package store

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUsersRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)

	t.Run("Insert+GetByID+GetByEmail", func(t *testing.T) {
		u := seedUser(ctx, t, tx)

		got, err := (UsersRepo{}).GetByID(ctx, tx, u.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Email != u.Email {
			t.Errorf("Email=%q want %q", got.Email, u.Email)
		}

		got2, err := (UsersRepo{}).GetByEmail(ctx, tx, u.Email)
		if err != nil {
			t.Fatalf("GetByEmail: %v", err)
		}
		if got2.ID != u.ID {
			t.Errorf("ID mismatch")
		}
	})

	t.Run("UpdatePassword+MarkEmailVerified", func(t *testing.T) {
		u := seedUser(ctx, t, tx)
		if err := (UsersRepo{}).UpdatePassword(ctx, tx, u.ID, "newhash"); err != nil {
			t.Fatalf("UpdatePassword: %v", err)
		}
		now := time.Now().UTC()
		if err := (UsersRepo{}).MarkEmailVerified(ctx, tx, u.ID, now); err != nil {
			t.Fatalf("MarkEmailVerified: %v", err)
		}
		got, err := (UsersRepo{}).GetByID(ctx, tx, u.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.PasswordHash != "newhash" {
			t.Errorf("password not updated: %q", got.PasswordHash)
		}
		if got.EmailVerifiedAt == nil {
			t.Error("EmailVerifiedAt nil")
		}
	})

	t.Run("GetByID NotFound", func(t *testing.T) {
		_, err := (UsersRepo{}).GetByID(ctx, tx, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})
}

func TestSessionsRepo(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	u := seedUser(ctx, t, tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	cases := []struct {
		name string
		hash [32]byte
	}{
		{"first", sha256.Sum256([]byte("token-1"))},
		{"second", sha256.Sum256([]byte("token-2"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{
				UserID:     u.ID,
				TokenHash:  tc.hash[:],
				ExpiresAt:  now.Add(time.Hour),
				LastSeenAt: now,
			}
			if err := (SessionsRepo{}).Insert(ctx, tx, s); err != nil {
				t.Fatalf("Insert: %v", err)
			}
			got, err := (SessionsRepo{}).GetActiveByTokenHash(ctx, tx, tc.hash[:], now)
			if err != nil {
				t.Fatalf("GetActiveByTokenHash: %v", err)
			}
			if got.ID != s.ID {
				t.Errorf("ID mismatch")
			}
		})
	}

	t.Run("Extend", func(t *testing.T) {
		hash := sha256.Sum256([]byte("token-ext"))
		s := &Session{UserID: u.ID, TokenHash: hash[:], ExpiresAt: now.Add(time.Hour), LastSeenAt: now}
		if err := (SessionsRepo{}).Insert(ctx, tx, s); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		newExp := now.Add(2 * time.Hour)
		if err := (SessionsRepo{}).Extend(ctx, tx, s.ID, newExp, now.Add(time.Minute)); err != nil {
			t.Fatalf("Extend: %v", err)
		}
		got, _ := (SessionsRepo{}).GetActiveByTokenHash(ctx, tx, hash[:], now)
		if !got.ExpiresAt.Equal(newExp) {
			t.Errorf("expires_at=%v want %v", got.ExpiresAt, newExp)
		}
	})

	t.Run("RevokeAllForUser", func(t *testing.T) {
		u2 := seedUser(ctx, t, tx)
		for _, s := range []string{"a", "b", "c"} {
			h := sha256.Sum256([]byte("revoke-" + s))
			ses := &Session{UserID: u2.ID, TokenHash: h[:], ExpiresAt: now.Add(time.Hour), LastSeenAt: now}
			if err := (SessionsRepo{}).Insert(ctx, tx, ses); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		n, err := (SessionsRepo{}).RevokeAllForUser(ctx, tx, u2.ID, now)
		if err != nil {
			t.Fatalf("RevokeAllForUser: %v", err)
		}
		if n != 3 {
			t.Errorf("revoked %d, want 3", n)
		}
	})
}

func TestEmailAndPasswordTokens(t *testing.T) {
	t.Parallel()
	ctx, tx := withTx(t)
	u := seedUser(ctx, t, tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("EmailVerificationTokens", func(t *testing.T) {
		hash := sha256.Sum256([]byte("verify-tok"))
		tok := &EmailVerificationToken{
			UserID:    u.ID,
			Email:     u.Email,
			TokenHash: hash[:],
			ExpiresAt: now.Add(time.Hour),
		}
		if err := (EmailVerificationTokensRepo{}).Insert(ctx, tx, tok); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, err := (EmailVerificationTokensRepo{}).GetActiveByTokenHash(ctx, tx, hash[:], now)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.UserID != u.ID {
			t.Error("user mismatch")
		}
		if err := (EmailVerificationTokensRepo{}).Consume(ctx, tx, got.ID, now); err != nil {
			t.Fatalf("Consume: %v", err)
		}
		_, err = (EmailVerificationTokensRepo{}).GetActiveByTokenHash(ctx, tx, hash[:], now)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("after consume want ErrNotFound, got %v", err)
		}
	})

	t.Run("PasswordResetTokens", func(t *testing.T) {
		hash := sha256.Sum256([]byte("reset-tok"))
		ip := "10.0.0.1"
		tok := &PasswordResetToken{
			UserID:      u.ID,
			TokenHash:   hash[:],
			RequestedIP: &ip,
			ExpiresAt:   now.Add(time.Hour),
		}
		if err := (PasswordResetTokensRepo{}).Insert(ctx, tx, tok); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, err := (PasswordResetTokensRepo{}).GetActiveByTokenHash(ctx, tx, hash[:], now)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.RequestedIP == nil || *got.RequestedIP != ip {
			t.Errorf("requested_ip=%v want %v", got.RequestedIP, ip)
		}
	})
}

