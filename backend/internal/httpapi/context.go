package httpapi

import (
	"context"

	"campusclaw/backend/internal/db"
)

type contextKey int

const sessionUserKey contextKey = iota

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
