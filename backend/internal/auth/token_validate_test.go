package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

var testNow = time.Date(2030, 3, 4, 5, 6, 7, 0, time.UTC)

// testIssuer returns an issuer with a frozen clock.
func testIssuer(secret string) *TokenIssuer {
	i := NewTokenIssuer(secret, 24*time.Hour)
	i.now = func() time.Time { return testNow }
	return i
}

// basePayload is a fully valid payload; each case mutates one key.
func basePayload() map[string]any {
	return map[string]any{
		"sub": "7",
		"jti": "0123456789abcdef0123456789abcdef",
		"iat": testNow.Unix(),
		"exp": testNow.Add(24 * time.Hour).Unix(),
		"iss": Issuer,
	}
}

// oldPayload is the previous token format: identity claims in the payload and
// no sub. Its signature is valid under the same key.
func oldPayload() map[string]any {
	return map[string]any{
		"user_id":  7,
		"username": "teacher_a",
		"role":     "teacher",
		"class_id": 1,
		"jti":      "0123456789abcdef0123456789abcdef",
		"iat":      testNow.Unix(),
		"exp":      testNow.Add(24 * time.Hour).Unix(),
		"iss":      Issuer,
	}
}

func signMap(t *testing.T, method jwt.SigningMethod, key string, payload map[string]any) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, jwt.MapClaims(payload)).SignedString([]byte(key))
	if err != nil {
		t.Fatalf("sign fixture: %v", err)
	}
	return signed
}

// reencode replaces the payload segment with mutated JSON but keeps the
// original signature, modelling an attacker editing claims in place.
func reencode(t *testing.T, token string, mutate func(map[string]any)) string {
	t.Helper()
	parts := splitToken(t, token)
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	mutate(m)
	edited, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(edited)
	return strings.Join(parts, ".")
}

// unsignedNone builds an alg=none token by hand.
func unsignedNone(t *testing.T) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	payload, err := json.Marshal(basePayload())
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
}

// tweaked signs a copy of the valid payload with one key changed (or deleted).
func tweaked(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	m := basePayload()
	mutate(m)
	return signMap(t, jwt.SigningMethodHS256, testSecret, m)
}

func TestParseMatrix(t *testing.T) {
	issuer := testIssuer(testSecret)
	valid := signMap(t, jwt.SigningMethodHS256, testSecret, basePayload())

	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"valid token", valid, false},
		{"signed with another key", signMap(t, jwt.SigningMethodHS256, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", basePayload()), true},
		{"role added, signature not recomputed", reencode(t, valid, func(m map[string]any) {
			m["role"] = "teacher"
		}), true},
		{"sub changed, signature not recomputed", reencode(t, valid, func(m map[string]any) {
			m["sub"] = "8"
		}), true},
		{"alg=none", unsignedNone(t), true},
		{"HS512", signMap(t, jwt.SigningMethodHS512, testSecret, basePayload()), true},
		{"expired", tweaked(t, func(m map[string]any) {
			m["exp"] = testNow.Add(-time.Minute).Unix()
		}), true},
		{"issuer mismatch", tweaked(t, func(m map[string]any) {
			m["iss"] = "other"
		}), true},
		{"missing sub", tweaked(t, func(m map[string]any) {
			delete(m, "sub")
		}), true},
		{"missing jti", tweaked(t, func(m map[string]any) {
			delete(m, "jti")
		}), true},
		{"missing exp", tweaked(t, func(m map[string]any) {
			delete(m, "exp")
		}), true},
		{"sub not a number", tweaked(t, func(m map[string]any) {
			m["sub"] = "abc"
		}), true},
		{"sub zero", tweaked(t, func(m map[string]any) {
			m["sub"] = "0"
		}), true},
		{"sub negative", tweaked(t, func(m map[string]any) {
			m["sub"] = "-1"
		}), true},
		{"sub as JSON number", tweaked(t, func(m map[string]any) {
			m["sub"] = 7
		}), true},
		{"jti empty", tweaked(t, func(m map[string]any) {
			m["jti"] = ""
		}), true},
		{"old format token, signature valid", signMap(t, jwt.SigningMethodHS256, testSecret, oldPayload()), true},
		{"not three segments", "abc.def", true},
		{"garbage", "not-a-token", true},
		{"empty", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := issuer.Parse(tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%s) succeeded, want error", tc.name)
				}
				// The error must never echo the token it rejected.
				if tc.token != "" && strings.Contains(err.Error(), tc.token) {
					t.Errorf("error text contains the token: %v", err)
				}
				if segs := strings.Split(tc.token, "."); len(segs) == 3 && segs[1] != "" &&
					strings.Contains(err.Error(), segs[1]) {
					t.Errorf("error text contains the payload segment: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%s) = %v, want success", tc.name, err)
			}
			if claims.UserID != 7 {
				t.Errorf("UserID = %d, want 7", claims.UserID)
			}
			if claims.JTI != "0123456789abcdef0123456789abcdef" {
				t.Errorf("JTI = %q, want the fixture jti", claims.JTI)
			}
			if !claims.ExpiresAt.Equal(testNow.Add(24 * time.Hour)) {
				t.Errorf("ExpiresAt = %v, want %v", claims.ExpiresAt, testNow.Add(24*time.Hour))
			}
		})
	}
}

// TestParseAcceptsRoundTrip guards against over-rejection: anything the issuer
// signs with its own clock and secret must parse back unchanged.
func TestParseAcceptsRoundTrip(t *testing.T) {
	issuer := testIssuer(testSecret)

	token, err := issuer.Issue(42)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parsed, err := issuer.Parse(token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.UserID != 42 {
		t.Errorf("UserID = %d, want 42", parsed.UserID)
	}
	if len(parsed.JTI) != 32 {
		t.Errorf("JTI = %q, want 32 hex characters", parsed.JTI)
	}
	if !parsed.ExpiresAt.Equal(testNow.Add(24 * time.Hour)) {
		t.Errorf("ExpiresAt = %v, want %v", parsed.ExpiresAt, testNow.Add(24*time.Hour))
	}
}

// TestParseExpiryUsesInjectedClock proves expiry is judged against the injected
// clock rather than the wall clock: a token that is expired "now" is accepted
// when the clock is moved back before its exp.
func TestParseExpiryUsesInjectedClock(t *testing.T) {
	signer := testIssuer(testSecret)
	token, err := signer.Issue(1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	verifier := testIssuer(testSecret)
	verifier.now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if _, err := verifier.Parse(token); err == nil {
		t.Fatal("Parse accepted an expired token, want error")
	}

	verifier.now = func() time.Time { return testNow.Add(time.Hour) }
	if _, err := verifier.Parse(token); err != nil {
		t.Fatalf("Parse rejected a live token: %v", err)
	}
}
