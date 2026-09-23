package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// NewSessionID returns a random, unguessable session identifier (>= 32
// bytes of entropy, per design.md Decision 2).
func NewSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// DeleteSessionsForUser removes every existing session for a user, so that a
// fresh login invalidates any session ID the client previously carried
// (prevents session fixation, see design.md Decision 2).
func DeleteSessionsForUser(ctx context.Context, conn *sql.DB, userID int) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete sessions for user: %w", err)
	}
	return nil
}

func CreateSession(ctx context.Context, conn *sql.DB, sessionID string, userID int, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	if _, err := conn.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, sessionID, userID, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetSessionUser resolves a session ID to its user, role and class, joining
// against users and classes. Returns ErrNotFound if the session does not
// exist or has expired.
func GetSessionUser(ctx context.Context, conn *sql.DB, sessionID string) (*SessionUser, error) {
	row := conn.QueryRowContext(ctx, `
		SELECT s.id, u.id, u.username, u.role, u.class_id, c.name
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN classes c ON c.id = u.class_id
		WHERE s.id = ? AND s.expires_at > NOW()`, sessionID)

	var su SessionUser
	if err := row.Scan(&su.SessionID, &su.UserID, &su.Username, &su.Role, &su.ClassID, &su.ClassName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session user: %w", err)
	}
	return &su, nil
}

func DeleteSession(ctx context.Context, conn *sql.DB, sessionID string) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
