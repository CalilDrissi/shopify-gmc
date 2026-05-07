package auth

import (
	"context"

	"github.com/google/uuid"
)

type User struct {
	ID    uuid.UUID
	Email string
	Name  string
}

type ctxKey int

const (
	userKey ctxKey = iota + 1
	sessionKey
)

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey).(User)
	return u, ok
}

func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

func SessionFromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionKey).(Session)
	return s, ok
}
