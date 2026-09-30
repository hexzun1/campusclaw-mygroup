package httpapi

import (
	"context"
	"time"

	"campusclaw/backend/internal/db"
)

type contextKey int

const (
	sessionUserKey contextKey = iota
	tokenMetaKey
)

// WithSessionUser attaches the authenticated session to the request context.
// This is the ONLY place role and class_id are supposed to come from for the
// rest of a request's handling (see design.md Decision 2 and 4).
func WithSessionUser(ctx context.Context, su *db.SessionUser) context.Context {
	return context.WithValue(ctx, sessionUserKey, su)
}

// SessionUserFromContext returns the session attached by the auth
// middleware, or nil if the request is unauthenticated.
func SessionUserFromContext(ctx context.Context) *db.SessionUser {
	su, _ := ctx.Value(sessionUserKey).(*db.SessionUser)
	return su
}

// TokenMeta is the per-token data a handler needs to revoke the exact token
// that authenticated the request: logout stores the jti until the token's own
// expiry. Identity stays in SessionUser; this only carries token bookkeeping.
type TokenMeta struct {
	JTI       string
	ExpiresAt time.Time
}

func WithTokenMeta(ctx context.Context, meta *TokenMeta) context.Context {
	return context.WithValue(ctx, tokenMetaKey, meta)
}

// TokenMetaFromContext returns the token bookkeeping attached by the auth
// middleware, or nil if the request is unauthenticated.
func TokenMetaFromContext(ctx context.Context) *TokenMeta {
	meta, _ := ctx.Value(tokenMetaKey).(*TokenMeta)
	return meta
}
