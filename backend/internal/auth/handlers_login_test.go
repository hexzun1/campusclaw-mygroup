package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"campusclaw/backend/internal/db"
	"campusclaw/backend/internal/httpapi"
)

const (
	loginTestCorrectPassword = "correct-password"
	loginTestWrongPassword   = "wrong-password"
)

type loginTestClock struct {
	now time.Time
}

func (c *loginTestClock) Now() time.Time { return c.now }

func (c *loginTestClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// newLoginTestHandlers wires Login to an in-memory user table and a limiter
// with a controlled clock. It returns a pointer to the lookup counter so a
// test can prove that rejected client addresses never reach the password path.
func newLoginTestHandlers(t *testing.T, usernames []string, trusted []netip.Prefix) (*Handlers, *loginTestClock, *int) {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(loginTestCorrectPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}

	users := make(map[string]*db.User, len(usernames))
	for i, name := range usernames {
		users[name] = &db.User{
			ID:           i + 1,
			Username:     name,
			PasswordHash: string(hash),
			Role:         db.RoleTeacher,
			ClassID:      1,
		}
	}

	clock := &loginTestClock{now: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	lookups := 0
	h := &Handlers{
		Issuer:         NewTokenIssuer(strings.Repeat("k", 32), 24*time.Hour),
		Limiter:        newLoginLimiter(5, 300, clock.Now),
		TrustedProxies: trusted,
		lookupUser: func(_ context.Context, _ *sql.DB, username string) (*db.User, error) {
			lookups++
			u, ok := users[username]
			if !ok {
				return nil, db.ErrNotFound
			}
			return u, nil
		},
	}
	return h, clock, &lookups
}

type loginTestAttempt struct {
	remote   string
	realIPs  []string
	headers  map[string][]string
	username string
	password string
}

func attemptLoginForTest(t *testing.T, h *Handlers, a loginTestAttempt) *httptest.ResponseRecorder {
	t.Helper()

	payload, err := json.Marshal(loginRequest{Username: a.username, Password: a.password})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(payload))
	r.RemoteAddr = a.remote
	for _, ip := range a.realIPs {
		r.Header.Add("X-Real-IP", ip)
	}
	for name, values := range a.headers {
		for _, value := range values {
			r.Header.Add(name, value)
		}
	}

	w := httptest.NewRecorder()
	h.Login(w, r)
	return w
}

func requireLoginStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, want, rec.Body.String())
	}
}

func loginResponseBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response %q: %v", rec.Body.String(), err)
	}
	return body
}

func requireLoginToken(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	token, _ := loginResponseBody(t, rec)["token"].(string)
	if token == "" {
		t.Fatalf("login response has no token: %s", rec.Body.String())
	}
}

func requireBadCredentials(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	requireLoginStatus(t, rec, http.StatusUnauthorized)
	body := loginResponseBody(t, rec)
	if _, ok := body["token"]; ok {
		t.Fatalf("401 response contains a token: %s", rec.Body.String())
	}
	if body["error"] != httpapi.MsgBadCredentials {
		t.Fatalf("error = %v, want %q", body["error"], httpapi.MsgBadCredentials)
	}
}

func prefixForLoginTest(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", s, err)
	}
	return p
}

// limiterFailuresForTest reads a bucket directly; tests are sequential, but
// the limiter lock is still taken so the check documents the safe pattern.
func limiterFailuresForTest(h *Handlers, key string) (int, bool) {
	h.Limiter.mu.Lock()
	defer h.Limiter.mu.Unlock()
	st, ok := h.Limiter.state[key]
	if !ok {
		return 0, false
	}
	return st.failures, true
}

func directAttempt(remote, username, password string, headers map[string][]string) loginTestAttempt {
	return loginTestAttempt{remote: remote, headers: headers, username: username, password: password}
}

func proxiedAttempt(proxy, clientIP, username, password string) loginTestAttempt {
	return loginTestAttempt{remote: proxy, realIPs: []string{clientIP}, username: username, password: password}
}

// TestLoginTrustedProxyClientsKeepSeparateBuckets mirrors the 3+2 scenario:
// failures from two real client IPs behind one trusted proxy never merge, and
// C2's success does not clear C1's count.
func TestLoginTrustedProxyClientsKeepSeparateBuckets(t *testing.T) {
	proxy := "192.0.2.1:5000"
	c1, c2 := "198.51.100.10", "198.51.100.20"
	h, _, _ := newLoginTestHandlers(t, []string{"alice"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	for i := 0; i < 3; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
	}
	for i := 0; i < 2; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestWrongPassword)))
	}

	rec := attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestCorrectPassword))
	requireLoginStatus(t, rec, http.StatusOK)
	requireLoginToken(t, rec)

	// C2 is now reset; two more failures on C1 reach its own threshold.
	for i := 0; i < 2; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
	}
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestCorrectPassword)))

	// C2 is still usable: the proxy address is not a shared bucket and C1's
	// lock did not leak across real client IPs.
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestCorrectPassword)), http.StatusOK)
}

// TestLoginAttackerCannotLockVictim covers the third-party lockout scenario:
// C1's five failures lock only C1's bucket, C2's correct password still works,
// and the locked response is the same generic 401 as a wrong password.
func TestLoginAttackerCannotLockVictim(t *testing.T) {
	proxy := "192.0.2.1:5000"
	c1, c2 := "198.51.100.10", "198.51.100.20"
	h, _, _ := newLoginTestHandlers(t, []string{"alice"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	for i := 0; i < 5; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
	}
	locked := attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestCorrectPassword))
	requireBadCredentials(t, locked)

	victim := attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestCorrectPassword))
	requireLoginStatus(t, victim, http.StatusOK)
	requireLoginToken(t, victim)

	// The lock message must not differ from the ordinary credential failure.
	wrong := attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestWrongPassword))
	requireBadCredentials(t, wrong)
	if locked.Body.String() != wrong.Body.String() {
		t.Fatalf("locked body = %q, want the same generic body as a wrong password %q", locked.Body.String(), wrong.Body.String())
	}
}

// TestLoginSameIPDifferentUsernamesAreIsolated covers one client with two
// accounts: locking U must not affect V, and V's success must not unlock U.
func TestLoginSameIPDifferentUsernamesAreIsolated(t *testing.T) {
	proxy := "192.0.2.1:5000"
	client := "198.51.100.10"
	h, _, _ := newLoginTestHandlers(t, []string{"alice", "bob"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	for i := 0; i < 5; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, client, "alice", loginTestWrongPassword)))
	}

	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, client, "bob", loginTestCorrectPassword)), http.StatusOK)
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, client, "alice", loginTestCorrectPassword)))
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, client, "bob", loginTestCorrectPassword)), http.StatusOK)
}

// TestLoginSuccessResetsOnlyItsBucketAndLockBoundary uses the controlled
// clock: C1's success clears only C1; C2 remains at four failures, locks on
// its fifth, is rejected at the 299th second and succeeds at the 300th.
func TestLoginSuccessResetsOnlyItsBucketAndLockBoundary(t *testing.T) {
	proxy := "192.0.2.1:5000"
	c1, c2 := "198.51.100.10", "198.51.100.20"
	h, clock, _ := newLoginTestHandlers(t, []string{"alice"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	for i := 0; i < 4; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestWrongPassword)))
	}

	// C1 succeeds and its counter is cleared; a single new failure must not
	// lock it, while C2 is untouched and still one failure from the threshold.
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestCorrectPassword)), http.StatusOK)
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestCorrectPassword)), http.StatusOK)

	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestWrongPassword)))

	clock.Advance(299 * time.Second)
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestCorrectPassword)))

	clock.Advance(time.Second) // exactly 300 seconds since C2's lock began
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c2, "alice", loginTestCorrectPassword)), http.StatusOK)
}

// TestLoginInvalidProxyHeaderRejectedBeforeLimiterOrLookup covers the trusted
// proxy header errors: 400, no token, no user lookup, and no failure added to
// any bucket. The trailing valid login must succeed, proving the earlier bad
// requests did not become the fifth failure.
func TestLoginInvalidProxyHeaderRejectedBeforeLimiterOrLookup(t *testing.T) {
	proxy := "192.0.2.1:5000"
	c1 := "198.51.100.10"
	h, _, lookups := newLoginTestHandlers(t, []string{"alice"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	for i := 0; i < 4; i++ {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestWrongPassword)))
	}
	*lookups = 0

	cases := []struct {
		name   string
		realIP []string
	}{
		{"missing", nil},
		{"empty", []string{""}},
		{"whitespace", []string{"   "}},
		{"duplicate", []string{"198.51.100.11", "198.51.100.12"}},
		{"comma list", []string{"198.51.100.11, 198.51.100.12"}},
		{"port", []string{"198.51.100.11:1234"}},
		{"zone", []string{"fe80::1%eth0"}},
		{"arbitrary text", []string{"not-an-ip"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := attemptLoginForTest(t, h, loginTestAttempt{
				remote:   proxy,
				realIPs:  tc.realIP,
				username: "alice",
				password: loginTestCorrectPassword,
			})
			requireLoginStatus(t, rec, http.StatusBadRequest)
			if _, ok := loginResponseBody(t, rec)["token"]; ok {
				t.Fatalf("400 response contains a token: %s", rec.Body.String())
			}
		})
	}
	if *lookups != 0 {
		t.Fatalf("user lookup ran %d times for rejected addresses, want 0", *lookups)
	}
	key := clientKey("alice", netip.MustParseAddr(c1))
	if failures, ok := limiterFailuresForTest(h, key); !ok || failures != 4 {
		t.Fatalf("bucket after rejected addresses = (%d, %v), want the original 4 failures", failures, ok)
	}

	// Four failures, no additions from the malformed requests: a valid login
	// is the fifth attempt but not a failure, so it succeeds and resets.
	requireLoginStatus(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, c1, "alice", loginTestCorrectPassword)), http.StatusOK)
}

// TestLoginDirectClientCannotForgeForwardingHeaders covers a direct client
// outside the trust list: changing X-Real-IP, X-Forwarded-For and Forwarded on
// every attempt must not change the bucket, and must not lock the victim IP.
func TestLoginDirectClientCannotForgeForwardingHeaders(t *testing.T) {
	c1 := "198.51.100.10:40001"
	c2 := "198.51.100.20:40002"
	h, _, _ := newLoginTestHandlers(t, []string{"alice"}, nil)

	for i := 0; i < 5; i++ {
		headers := map[string][]string{
			"X-Real-IP":       {netip.AddrFrom4([4]byte{203, 0, 113, byte(i + 1)}).String()},
			"X-Forwarded-For": {"203.0.113.99, 198.51.100.20"},
			"Forwarded":       {"for=198.51.100.20"},
		}
		requireBadCredentials(t, attemptLoginForTest(t, h, directAttempt(c1, "alice", loginTestWrongPassword, headers)))
	}

	requireBadCredentials(t, attemptLoginForTest(t, h, directAttempt(c1, "alice", loginTestCorrectPassword, nil)))
	requireLoginStatus(t, attemptLoginForTest(t, h, directAttempt(c2, "alice", loginTestCorrectPassword, nil)), http.StatusOK)
}

// TestLoginDifferentSourcePortsShareBucket: the same direct IP with a changing
// source port gets one bucket.
func TestLoginDifferentSourcePortsShareBucket(t *testing.T) {
	h, _, _ := newLoginTestHandlers(t, []string{"alice"}, nil)
	for i := 0; i < 5; i++ {
		remote := "198.51.100.10:" + strconv.Itoa(40000+i)
		requireBadCredentials(t, attemptLoginForTest(t, h, directAttempt(remote, "alice", loginTestWrongPassword, nil)))
	}
	requireBadCredentials(t, attemptLoginForTest(t, h, directAttempt("198.51.100.10:60000", "alice", loginTestCorrectPassword, nil)))
}

// TestLoginEquivalentAddressFormsShareBucket: IPv4 vs IPv4-mapped IPv6 and
// compressed vs expanded IPv6 spellings of the same client share one bucket.
func TestLoginEquivalentAddressFormsShareBucket(t *testing.T) {
	proxy := "192.0.2.1:5000"
	h, _, _ := newLoginTestHandlers(t, []string{"alice", "bob"}, []netip.Prefix{prefixForLoginTest(t, "192.0.2.1/32")})

	mapped := []string{"203.0.113.5", "203.0.113.5", "::ffff:203.0.113.5", "::ffff:203.0.113.5", "203.0.113.5"}
	for _, ip := range mapped {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, ip, "alice", loginTestWrongPassword)))
	}
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, "203.0.113.5", "alice", loginTestCorrectPassword)))

	v6 := []string{
		"2001:db8::1",
		"2001:0db8:0000:0000:0000:0000:0000:0001",
		"2001:db8::1",
		"2001:0db8:0000:0000:0000:0000:0000:0001",
		"2001:db8::1",
	}
	for _, ip := range v6 {
		requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, ip, "bob", loginTestWrongPassword)))
	}
	requireBadCredentials(t, attemptLoginForTest(t, h, proxiedAttempt(proxy, "2001:db8::1", "bob", loginTestCorrectPassword)))
}

// TestLoginUnparseableRemoteAddrRejected: a direct peer that is not a literal
// IP is a 400 before lookup, and a forged X-Real-IP cannot rescue it.
func TestLoginUnparseableRemoteAddrRejected(t *testing.T) {
	h, _, lookups := newLoginTestHandlers(t, []string{"alice"}, nil)

	rec := attemptLoginForTest(t, h, loginTestAttempt{
		remote:   "not-an-ip",
		realIPs:  []string{"198.51.100.20"},
		username: "alice",
		password: loginTestCorrectPassword,
	})
	requireLoginStatus(t, rec, http.StatusBadRequest)
	if _, ok := loginResponseBody(t, rec)["token"]; ok {
		t.Fatalf("400 response contains a token: %s", rec.Body.String())
	}
	if *lookups != 0 {
		t.Fatalf("user lookup ran %d times for an unparseable peer, want 0", *lookups)
	}
	h.Limiter.mu.Lock()
	buckets := len(h.Limiter.state)
	h.Limiter.mu.Unlock()
	if buckets != 0 {
		t.Fatalf("limiter has %d buckets after an unparseable peer, want 0", buckets)
	}

	requireLoginStatus(t, attemptLoginForTest(t, h, directAttempt("198.51.100.10:1234", "alice", loginTestCorrectPassword, nil)), http.StatusOK)
}
