package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

type Handlers struct {
	Conn    *sql.DB
	Issuer  *TokenIssuer
	Limiter *LoginLimiter
}

func NewHandlers(conn *sql.DB, issuer *TokenIssuer, limiter *LoginLimiter) *Handlers {
	return &Handlers{Conn: conn, Issuer: issuer, Limiter: limiter}
}

// dummyHash is compared against when a username does not exist, so lookup
// time is close to a real password check and usernames cannot be enumerated
// by timing (design.md Decision 3).
var dummyHash = mustHash("this-is-not-a-real-password-used-only-for-timing")

func mustHash(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func clientKey(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return username + "|" + host
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	key := clientKey(r, req.Username)

	if h.Limiter.Locked(key) {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgBadCredentials)
		return
	}

	user, err := db.GetUserByUsername(r.Context(), h.Conn, req.Username)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	if err != nil { // ErrNotFound
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		h.Limiter.RecordFailure(key)
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgBadCredentials)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		h.Limiter.RecordFailure(key)
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgBadCredentials)
		return
	}

	h.Limiter.Reset(key)

	// A successful login signs a self-contained token: no server-side session
	// record is written and no cookie is set. Existing tokens of the same user
	// stay valid until they expire or are revoked by logout (design.md
	// Decision 6).
	token, err := h.Issuer.Issue(user.ID, user.Username, string(user.Role), user.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "内部错误")
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]string{
		"token":    token,
		"username": user.Username,
		"role":     string(user.Role),
	})
}

// Logout revokes exactly the token that authenticated the request. Other
// tokens of the same user are untouched (design.md Decision 3). The request
// already passed RequireAuth, so an invalid or already revoked token never
// reaches this handler.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	meta := httpapi.TokenMetaFromContext(r.Context())
	if su == nil || meta == nil {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
		return
	}

	// The revocation must still land if the client disconnects mid-request.
	ctx := context.WithoutCancel(r.Context())
	if err := db.RevokeToken(ctx, h.Conn, meta.JTI, su.UserID, meta.ExpiresAt); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	// Opportunistic cleanup: revocation rows are only meaningful until their
	// token expires. A failure here MUST NOT fail the logout itself, since the
	// token is already revoked.
	if err := db.PurgeExpiredRevocations(ctx, h.Conn); err != nil {
		log.Printf("purge expired revocations: %v", err)
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	if su == nil {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
		return
	}

	// The token carries the identity but not the class name, so this is the one
	// field resolved from the database (design.md Decision 2).
	class, err := db.GetClassByID(r.Context(), h.Conn, su.ClassID)
	if err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"username":   su.Username,
		"role":       su.Role,
		"class_id":   su.ClassID,
		"class_name": class.Name,
	})
}
