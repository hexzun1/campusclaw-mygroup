package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

type Handlers struct {
	Conn       *sql.DB
	SessionTTL time.Duration
	Limiter    *LoginLimiter
}

func NewHandlers(conn *sql.DB, sessionTTL time.Duration, limiter *LoginLimiter) *Handlers {
	return &Handlers{Conn: conn, SessionTTL: sessionTTL, Limiter: limiter}
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

	// Issue a brand-new session and invalidate any session the caller
	// already had, so an old session ID can never resurrect (design.md
	// Decision 2: prevents session fixation).
	if err := db.DeleteSessionsForUser(r.Context(), h.Conn, user.ID); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}
	sessionID, err := db.NewSessionID()
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	if err := db.CreateSession(r.Context(), h.Conn, sessionID, user.ID, h.SessionTTL); err != nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
		return
	}

	SetSessionCookie(w, sessionID, h.SessionTTL)
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{
		"username": user.Username,
		"role":     string(user.Role),
	})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if sessionID, ok := ReadSessionCookie(r); ok {
		_ = db.DeleteSession(context.WithoutCancel(r.Context()), h.Conn, sessionID)
	}
	ClearSessionCookie(w)
	httpapi.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	if su == nil {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"username":   su.Username,
		"role":       su.Role,
		"class_id":   su.ClassID,
		"class_name": su.ClassName,
	})
}
