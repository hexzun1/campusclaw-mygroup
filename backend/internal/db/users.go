package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

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
