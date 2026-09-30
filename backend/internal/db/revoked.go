package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// revokedTokensDDL creates the logout revocation table. The api executes it
// idempotently at startup instead of shipping a migrations/*.sql file: the
// MySQL image only runs initdb.d on an empty data volume, and the migration
// runner belongs to another change (design.md Decision 3).
const revokedTokensDDL = `
CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti CHAR(32) PRIMARY KEY,
    user_id INT NOT NULL,
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_revoked_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

// EnsureRevokedTokens creates revoked_tokens when it does not exist yet.
// Running it on every startup MUST be safe and MUST NOT alter an existing
// table.
func EnsureRevokedTokens(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, revokedTokensDDL); err != nil {
		return fmt.Errorf("ensure revoked_tokens: %w", err)
	}
	return nil
}

// RevokeToken records a token's jti as revoked until the token's own expiry.
// INSERT IGNORE makes a repeated logout of the same token a no-op instead of
// an error.
func RevokeToken(ctx context.Context, conn *sql.DB, jti string, userID int, expiresAt time.Time) error {
	if _, err := conn.ExecContext(ctx,
		`INSERT IGNORE INTO revoked_tokens (jti, user_id, expires_at) VALUES (?, ?, ?)`,
		jti, userID, expiresAt); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// IsRevoked reports whether the jti has been recorded as revoked. Rows are
// only meaningful until their token expires, but no expiry condition is added
// here: an expired token is already rejected by its own exp check, so the
// extra predicate could only introduce a dependency on the database clock.
// Expired rows are removed by PurgeExpiredRevocations.
func IsRevoked(ctx context.Context, conn *sql.DB, jti string) (bool, error) {
	var one int
	err := conn.QueryRowContext(ctx,
		`SELECT 1 FROM revoked_tokens WHERE jti = ?`, jti).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check revocation: %w", err)
	}
	return true, nil
}

// PurgeExpiredRevocations drops revocation rows whose token has already
// expired: from that moment on the token is rejected by signature validation
// alone, so the row is no longer needed. Callers run it as opportunistic
// cleanup on logout.
func PurgeExpiredRevocations(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx,
		`DELETE FROM revoked_tokens WHERE expires_at < NOW()`); err != nil {
		return fmt.Errorf("purge expired revocations: %w", err)
	}
	return nil
}
