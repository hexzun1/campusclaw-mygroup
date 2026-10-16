package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer is the only accepted `iss` claim value (design.md Decision 1).
const Issuer = "campusclaw-api"

// SigningMethod is the only accepted algorithm: HS256. Anything else, including
// alg=none and HS384/HS512, is rejected by the parser's method whitelist.
var SigningMethod = jwt.SigningMethodHS256

// payloadClaims is the exact key set a token payload may carry. Any extra key —
// the old format's user_id/username/role/class_id in particular — makes the
// token invalid, so a token from the previous format is rejected on its key
// set even though its signature is valid.
var payloadClaims = []string{"sub", "jti", "iat", "exp", "iss"}

// Token is everything later steps take from a verified bearer token: the user
// id every identity lookup starts from, plus the bookkeeping the revocation
// table needs (jti and the token's own expiry).
type Token struct {
	UserID    int
	JTI       string
	ExpiresAt time.Time
}

// TokenIssuer signs bearer tokens with HS256. The clock is injectable so tests
// can exercise expiry without waiting.
type TokenIssuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewTokenIssuer(secret string, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), ttl: ttl, now: time.Now}
}

// Issue signs a token whose payload is exactly sub, jti, iat, exp and iss. The
// identity is only the user ID: role and class are resolved from the database
// on every request, so no token can carry a stale identity and none is
// invalidated by a role or class change (design.md Decision 1).
func (i *TokenIssuer) Issue(userID int) (string, error) {
	jti, err := newJTI()
	if err != nil {
		return "", err
	}

	now := i.now()
	token := jwt.NewWithClaims(SigningMethod, jwt.RegisteredClaims{
		Subject:   strconv.Itoa(userID),
		ID:        jti,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		Issuer:    Issuer,
	})

	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// newJTI returns a 128-bit random identifier as 32 hex characters, matching
// revoked_tokens.jti CHAR(32).
func newJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Parse verifies a compact token and returns the identity it proves. The
// parser is a whitelist (design.md Decision 1): only HS256, `exp` required and
// unexpired, `iss` matching, and the payload's key set exactly payloadClaims
// with `sub` a positive integer and `jti` non-empty. This step is pure
// computation — the database is not consulted. Every failure is reported as an
// error that never contains the token itself.
func (i *TokenIssuer) Parse(raw string) (*Token, error) {
	claims := jwt.MapClaims{}

	_, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		return i.secret, nil
	},
		jwt.WithValidMethods([]string{SigningMethod.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(Issuer),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	if err := checkPayloadClaims(claims); err != nil {
		return nil, err
	}

	// The type assertion is deliberate: `sub` must be a decimal string, so a
	// numeric or otherwise typed value is rejected.
	sub, ok := claims["sub"].(string)
	if !ok || !isDecimal(sub) {
		return nil, errors.New("invalid token: sub is not a positive integer")
	}
	userID, err := strconv.Atoi(sub)
	if err != nil || userID <= 0 {
		return nil, errors.New("invalid token: sub is not a positive integer")
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil, errors.New("invalid token: missing jti")
	}

	// WithExpirationRequired already rejected a missing or malformed exp; the
	// value is re-read here for the revocation row.
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return nil, errors.New("invalid token: missing exp")
	}

	return &Token{UserID: userID, JTI: jti, ExpiresAt: exp.Time}, nil
}

// checkPayloadClaims requires the payload's key set to be exactly
// payloadClaims: an old-format token carries extra keys and no `sub`, and both
// directions of that difference are invalid.
func checkPayloadClaims(claims jwt.MapClaims) error {
	if len(claims) != len(payloadClaims) {
		return errors.New("invalid token: unexpected claim set")
	}
	for _, key := range payloadClaims {
		if _, ok := claims[key]; !ok {
			return fmt.Errorf("invalid token: missing %s claim", key)
		}
	}
	return nil
}

// isDecimal reports whether s is a non-empty run of ASCII digits. It is
// stricter than strconv.Atoi, which also accepts a sign prefix.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
