package auth

import (
	"database/sql"
	"errors"
	"net/http"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

// RequireSession resolves the session cookie into a db.SessionUser and
// attaches it to the request context. A missing/expired/unknown session
// returns 401 with no business data. A database connectivity failure returns
// 503 instead (design.md Decision 7: DB 不可用时业务接口返回 503, 而不是把已登录
// 用户判成 401), and MUST NOT clear the caller's cookie either way.
func RequireSession(conn *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sessionID, ok := ReadSessionCookie(r)
			if !ok {
				httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
				return
			}

			su, err := db.GetSessionUser(r.Context(), conn, sessionID)
			if err != nil {
				if errors.Is(err, db.ErrNotFound) {
					httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
					return
				}
				httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
				return
			}

			next.ServeHTTP(w, r.WithContext(httpapi.WithSessionUser(r.Context(), su)))
		})
	}
}
