package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// bearerPrefix is matched case-insensitively, so "bearer", "Bearer" and
// "BEARER" are all accepted.
const bearerPrefix = "bearer "

// bearerToken extracts the credential from the Authorization header. A token
// carried in a query parameter, a cookie or the request body is deliberately
// not looked at: only this header is a credential source.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// RequireAuth authenticates a request from its `Authorization: Bearer` token
// in four ordered steps (design.md Decision 1): ① take the bearer credential;
// ② verify it — pure computation, no database — ③ check the revocation table;
// ④ resolve the user row and its class from the database. Role and class are
// taken from step ④ only and never from the token, so they are always current.
//
// Every authentication failure — missing header, wrong scheme, malformed or
// invalid signature, expired, wrong issuer, wrong claim set, revoked jti, or a
// user that no longer exists — answers with the same 401 body, so callers
// cannot tell the reasons apart and no business data leaks. A database failure
// while checking revocation or the user row answers 503 instead of 401: a
// valid token MUST NOT be judged invalid just because a lookup could not run,
// and the request MUST NOT be let through either.
func RequireAuth(conn *sql.DB, issuer *TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			parsed, err := issuer.Parse(token)
			if err != nil {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			revoked, err := db.IsRevoked(r.Context(), conn, parsed.JTI)
			if err != nil {
				httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
				return
			}
			if revoked {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			su, err := db.GetIdentity(r.Context(), conn, parsed.UserID)
			if err != nil {
				if errors.Is(err, db.ErrNotFound) {
					httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				} else {
					httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
				}
				return
			}
			ctx := httpapi.WithSessionUser(r.Context(), su)

			// Logout needs the exact token identity to revoke it.
			meta := &httpapi.TokenMeta{JTI: parsed.JTI, ExpiresAt: parsed.ExpiresAt}
			ctx = httpapi.WithTokenMeta(ctx, meta)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireTeacher answers 403 unless the authenticated session is a teacher. It
// MUST be composed after RequireAuth, and it decides on the session alone —
// before the wrapped handler reads any request body — so a rejected request
// costs no parsing, no disk write and no database write (spec: 学生不能维护).
func RequireTeacher(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		su := httpapi.SessionUserFromContext(r.Context())
		if su == nil || su.Role != db.RoleTeacher {
			httpapi.WriteError(w, http.StatusForbidden, httpapi.MsgForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
