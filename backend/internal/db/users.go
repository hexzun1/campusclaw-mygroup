package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetIdentity resolves a user id from a verified token into the identity a
// request acts with, joined with the class name so no handler needs a second
// lookup. It is the per-request source of role and class: the token carries
// neither, so a database change takes effect on the very next request.
// Returns ErrNotFound when no such user exists.
func GetIdentity(ctx context.Context, conn *sql.DB, userID int) (*SessionUser, error) {
	row := conn.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.class_id, c.name
		FROM users u
		JOIN classes c ON c.id = u.class_id
		WHERE u.id = ?`, userID)

	var su SessionUser
	if err := row.Scan(&su.UserID, &su.Username, &su.Role, &su.ClassID, &su.ClassName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get identity: %w", err)
	}
	return &su, nil
}

// GetUserByUsername returns ErrNotFound if no such user exists.
func GetUserByUsername(ctx context.Context, conn *sql.DB, username string) (*User, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, username, password_hash, role, class_id FROM users WHERE username = ?`, username)

	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.ClassID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return &u, nil
}

func GetClassByID(ctx context.Context, conn *sql.DB, id int) (*Class, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, name FROM classes WHERE id = ?`, id)

	var c Class
	if err := row.Scan(&c.ID, &c.Name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get class by id: %w", err)
	}
	return &c, nil
}

func GetClassByName(ctx context.Context, conn *sql.DB, name string) (*Class, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, name FROM classes WHERE name = ?`, name)

	var c Class
	if err := row.Scan(&c.ID, &c.Name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get class by name: %w", err)
	}
	return &c, nil
}
