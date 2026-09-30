package auth

import (
	"database/sql"
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

// RequireAuth authenticates a request from its `Authorization: Bearer` token,
// checks the token against the revocation table, and attaches the identity to
// the request context (design.md Decisions 1, 2 and 3).
//
// Every authentication failure — missing header, wrong scheme, malformed or
// invalid signature, expired, wrong issuer, missing claims, revoked jti —
// answers with the same 401 body, so callers cannot tell the reasons apart and
// no business data leaks. A database failure while checking revocation answers
// 503 instead of 401: a valid token MUST NOT be judged invalid just because the
// lookup could not run, and the request MUST NOT be let through either.
func RequireAuth(conn *sql.DB, issuer *TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			claims, err := issuer.Parse(token)
			if err != nil {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			revoked, err := db.IsRevoked(r.Context(), conn, claims.ID)
			if err != nil {
				httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
				return
			}
			if revoked {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			// The identity comes from the verified token and nowhere else;
			// ClassName is not a claim and stays empty until a handler needs
			// it and looks it up by class_id.
			su := &db.SessionUser{
				UserID:   claims.UserID,
				Username: claims.Username,
				Role:     db.Role(claims.Role),
				ClassID:  claims.ClassID,
			}
			ctx := httpapi.WithSessionUser(r.Context(), su)

			// Logout needs the exact token identity to revoke it.
			meta := &httpapi.TokenMeta{JTI: claims.ID}
			if claims.ExpiresAt != nil {
				meta.ExpiresAt = claims.ExpiresAt.Time
			}
			ctx = httpapi.WithTokenMeta(ctx, meta)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
