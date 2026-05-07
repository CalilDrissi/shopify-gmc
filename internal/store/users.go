package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID              uuid.UUID
	Email           string
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Name            *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type UsersRepo struct{}

func (UsersRepo) Insert(ctx context.Context, q Querier, u *User) error {
	return translatePgErr(q.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at
	`, u.Email, u.PasswordHash, u.Name).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt))
}

func (UsersRepo) GetByID(ctx context.Context, q Querier, id uuid.UUID) (*User, error) {
	u := &User{}
	err := q.QueryRow(ctx, `
		SELECT id, email, email_verified_at, password_hash, name, created_at, updated_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Name, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return u, nil
}

func (UsersRepo) GetByEmail(ctx context.Context, q Querier, email string) (*User, error) {
	u := &User{}
	err := q.QueryRow(ctx, `
		SELECT id, email, email_verified_at, password_hash, name, created_at, updated_at
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Name, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, translatePgErr(err)
	}
	return u, nil
}

func (UsersRepo) UpdatePassword(ctx context.Context, q Querier, id uuid.UUID, newHash string) error {
	tag, err := q.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, newHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (UsersRepo) MarkEmailVerified(ctx context.Context, q Querier, id uuid.UUID, at time.Time) error {
	tag, err := q.Exec(ctx, `UPDATE users SET email_verified_at=$2, updated_at=now() WHERE id=$1`, id, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
