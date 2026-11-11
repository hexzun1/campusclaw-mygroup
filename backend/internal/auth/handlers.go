package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

type Handlers struct {
	Conn    *sql.DB
	Issuer  *TokenIssuer
	Limiter *LoginLimiter

	// TrustedProxies is the explicit list of direct TCP peers allowed to
	// supply X-Real-IP for the login rate-limit key. Empty means no peer is
	// trusted; see ClientIP.
	TrustedProxies []netip.Prefix

	// lookupUser is a test seam; nil uses the database lookup.
	lookupUser func(context.Context, *sql.DB, string) (*db.User, error)
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

// loginRequest is the accepted body of POST /api/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
} // @name LoginRequest

// clientKey builds the rate-limit bucket key from the username and the
// normalized client address. The username length prefix keeps the two parts
// unambiguous even when the username itself contains the separator.
func clientKey(username string, addr netip.Addr) string {
	return strconv.Itoa(len(username)) + ":" + username + "@" + addr.String()
}

// findUser resolves a username, allowing handler tests to substitute the
// database without a live MySQL instance.
func (h *Handlers) findUser(ctx context.Context, username string) (*db.User, error) {
	if h.lookupUser != nil {
		return h.lookupUser(ctx, h.Conn, username)
	}
	return db.GetUserByUsername(ctx, h.Conn, username)
}

// Login implements POST /api/login: it validates the credentials and answers
// with a bearer token.
//
// @Summary 登录
// @Description 用用户名与密码换取 Bearer token；响应中的 token 用于后续请求的 Authorization 头。无需认证。
// @Tags 认证
// @Accept json
// @Produce json
// @Param credentials body loginRequest true "用户名与密码"
// @Success 200 {object} map[string]string "token、username、role"
// @Failure 400 {object} httpapi.errorBody "请求格式错误或客户端地址不可识别"
// @Failure 401 {object} httpapi.errorBody "用户名或密码错误（含连续失败后的限流）"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Router /api/login [post]
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Username == "" || req.Password == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "用户名和密码不能为空")
		return
	}

	// The client address is resolved before any limiter or password work. A
	// trusted proxy that omits or malforms X-Real-IP (and an unparseable
	// direct peer) is a request error: 400 without touching a bucket.
	clientAddr, err := ClientIP(r, h.TrustedProxies)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "无法识别的客户端地址")
		return
	}

	key := clientKey(req.Username, clientAddr)

	if h.Limiter.Locked(key) {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgBadCredentials)
		return
	}

	user, err := h.findUser(r.Context(), req.Username)
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

	// Login exchange (design.md Decision 1): a bearer token that passes the
	// same checks as RequireAuth steps ①–③ — parse and revocation, without
	// requiring the user to still exist — is revoked before the new token is
	// signed, so rotating a token never leaves two live credentials behind.
	// A failure to write the revocation answers 503 and no token is issued.
	// A token that fails any check is ignored: it must not affect the login.
	if raw, ok := bearerToken(r); ok {
		if parsed, err := h.Issuer.Parse(raw); err == nil {
			revoked, err := db.IsRevoked(r.Context(), h.Conn, parsed.JTI)
			if err != nil {
				httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
				return
			}
			if !revoked {
				if err := db.RevokeToken(r.Context(), h.Conn, parsed.JTI, parsed.UserID, parsed.ExpiresAt); err != nil {
					httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.MsgServiceDown)
					return
				}
			}
		}
	}

	// A successful login signs a self-contained token: no server-side session
	// record is written and no cookie is set. Tokens issued without an existing
	// one presented stay valid until they expire or are revoked by logout.
	token, err := h.Issuer.Issue(user.ID)
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
//
// @Summary 登出
// @Description 吊销本次请求所用的这一个 token；同一用户的其他 token 不受影响。token 无效或已吊销时是 401。
// @Tags 认证
// @Produce json
// @Success 200 {object} map[string]bool "ok 恒为 true"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Failure 503 {object} httpapi.errorBody "服务暂不可用"
// @Security BearerAuth
// @Router /api/logout [post]
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

// @Summary 当前身份
// @Description 返回 token 对应的用户名、角色、班级编号与班级名。
// @Tags 认证
// @Produce json
// @Success 200 {object} map[string]interface{} "username、role、class_id、class_name"
// @Failure 401 {object} httpapi.errorBody "未登录或会话已失效"
// @Security BearerAuth
// @Router /api/me [get]
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	su := httpapi.SessionUserFromContext(r.Context())
	if su == nil {
		httpapi.WriteError(w, http.StatusUnauthorized, httpapi.MsgUnauthorized)
		return
	}

	// The whole identity — including the class name — was resolved from the
	// database by RequireAuth, so this handler reads the context only.
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"username":   su.Username,
		"role":       su.Role,
		"class_id":   su.ClassID,
		"class_name": su.ClassName,
	})
}
