package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"campusclaw/backend/internal/db"
)

// Issuer is the only accepted `iss` claim value (design.md Decision 1).
const Issuer = "campusclaw-api"

// SigningMethod is the only accepted algorithm: HS256. Anything else, including
// alg=none and HS384/HS512, is rejected by the parser's method whitelist.
var SigningMethod = jwt.SigningMethodHS256

// Claims is the token payload: the identity the API trusts, plus the registered
// claims. Only these eight keys are ever emitted (design.md Decision 2).
type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	ClassID  int    `json:"class_id"`

	jwt.RegisteredClaims
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

// Issue signs a token whose payload carries exactly the identity fields the
// spec allows. It MUST NOT include the password or its hash: only the values
// passed in here are written to the payload.
func (i *TokenIssuer) Issue(userID int, username, role string, classID int) (string, error) {
	jti, err := newJTI()
	if err != nil {
		return "", err
	}

	now := i.now()
	token := jwt.NewWithClaims(SigningMethod, Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		ClassID:  classID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
			ID:        jti,
		},
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

// Parse verifies a compact token and returns its claims. The parser is
// configured as a whitelist (design.md Decision 1): only HS256 is accepted,
// `exp` is mandatory, `iss` must match, and `iat` is checked. Claims that the
// registered set cannot express — jti, user_id, class_id, role — are validated
// afterwards. Every failure is reported as an error that never contains the
// token itself.
func (i *TokenIssuer) Parse(raw string) (*Claims, error) {
	var claims Claims

	_, err := jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) {
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

	if err := validateClaims(&claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

// validateClaims enforces the identity fields the spec requires. A token that
// parses and verifies but lacks any of them is still invalid.
func validateClaims(c *Claims) error {
	if c.ID == "" {
		return errors.New("invalid token: missing jti")
	}
	if c.UserID <= 0 {
		return errors.New("invalid token: missing user_id")
	}
	if c.ClassID <= 0 {
		return errors.New("invalid token: missing class_id")
	}
	switch db.Role(c.Role) {
	case db.RoleTeacher, db.RoleStudent:
	default:
		return errors.New("invalid token: unsupported role")
	}
	return nil
}
