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

// baseClaims is a fully valid payload; each case mutates one field.
func baseClaims() Claims {
	return Claims{
		UserID:   7,
		Username: "teacher_a",
		Role:     "teacher",
		ClassID:  1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(testNow),
			ExpiresAt: jwt.NewNumericDate(testNow.Add(24 * time.Hour)),
			ID:        "0123456789abcdef0123456789abcdef",
		},
	}
}

func signWith(t *testing.T, method jwt.SigningMethod, key string, c Claims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, c).SignedString([]byte(key))
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
	payload, err := json.Marshal(baseClaims())
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
}

func TestParseTokenMatrix(t *testing.T) {
	issuer := testIssuer(testSecret)
	valid := signWith(t, jwt.SigningMethodHS256, testSecret, baseClaims())
	studentToken := func() string {
		c := baseClaims()
		c.Role = "student"
		return signWith(t, jwt.SigningMethodHS256, testSecret, c)
	}()

	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"valid token", valid, false},
		{"valid token with student role", studentToken, false},
		{"signed with another key", signWith(t, jwt.SigningMethodHS256, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", baseClaims()), true},
		{"payload tampered, signature not recomputed", reencode(t, studentToken, func(m map[string]any) {
			m["role"] = "teacher"
		}), true},
		{"class_id tampered, signature not recomputed", reencode(t, studentToken, func(m map[string]any) {
			m["class_id"] = float64(2)
		}), true},
		{"alg=none", unsignedNone(t), true},
		{"HS512", signWith(t, jwt.SigningMethodHS512, testSecret, baseClaims()), true},
		{"expired", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.ExpiresAt = jwt.NewNumericDate(testNow.Add(-time.Minute))
			return c
		}()), true},
		{"issuer mismatch", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.Issuer = "other"
			return c
		}()), true},
		{"missing jti", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.ID = ""
			return c
		}()), true},
		{"missing exp", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.ExpiresAt = nil
			return c
		}()), true},
		{"missing role", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.Role = ""
			return c
		}()), true},
		{"unknown role", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.Role = "admin"
			return c
		}()), true},
		{"missing class_id", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.ClassID = 0
			return c
		}()), true},
		{"missing user_id", signWith(t, jwt.SigningMethodHS256, testSecret, func() Claims {
			c := baseClaims()
			c.UserID = 0
			return c
		}()), true},
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
			if claims.UserID != 7 || claims.ClassID != 1 || claims.Username != "teacher_a" {
				t.Errorf("claims = %+v, want the issued identity", claims)
			}
		})
	}
}

// TestParseAcceptsRoundTrip guards against over-rejection: anything the issuer
// signs with its own clock and secret must parse back unchanged.
func TestParseAcceptsRoundTrip(t *testing.T) {
	issuer := testIssuer(testSecret)

	token, err := issuer.Issue(42, "student_b1", "student", 2)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := issuer.Parse(token)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "student_b1" || claims.Role != "student" || claims.ClassID != 2 {
		t.Errorf("claims = %+v, want the issued identity", claims)
	}
	if claims.ID == "" || claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.Issuer != Issuer {
		t.Errorf("registered claims incomplete: %+v", claims.RegisteredClaims)
	}
}

// TestParseExpiryUsesInjectedClock proves expiry is judged against the injected
// clock rather than the wall clock: a token that is expired "now" is accepted
// when the clock is moved back before its exp.
func TestParseExpiryUsesInjectedClock(t *testing.T) {
	signer := testIssuer(testSecret)
	token, err := signer.Issue(1, "teacher_a", "teacher", 1)
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
