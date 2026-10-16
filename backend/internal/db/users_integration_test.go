package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestGetIdentityIntegration exercises the per-request identity lookup against
// a real MySQL: the JOIN with classes and the "no row means ErrNotFound"
// contract are enforced by SQL, not by Go.
func TestGetIdentityIntegration(t *testing.T) {
	conn := openTestDB(t)
	ctx := context.Background()

	classA, _, teacherA := fixture(t, conn)

	su, err := GetIdentity(ctx, conn, teacherA)
	if err != nil {
		t.Fatalf("GetIdentity(teacher_a): %v", err)
	}
	if su.UserID != teacherA || su.Username != "teacher_a" || su.Role != RoleTeacher ||
		su.ClassID != classA || su.ClassName != "A班" {
		t.Errorf("identity = %+v, want teacher_a/teacher/class %d/A班", su, classA)
	}

	if _, err := GetIdentity(ctx, conn, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIdentity(999999) = %v, want ErrNotFound", err)
	}
}

// TestGetIdentityDatabaseUnavailable proves a failing connection surfaces as a
// plain error, not as ErrNotFound: "no such user" must never be confused with
// "could not check" — the middleware turns the two into 401 and 503.
func TestGetIdentityDatabaseUnavailable(t *testing.T) {
	conn, err := sql.Open("mysql", "nobody:nothing@tcp(127.0.0.1:1)/campusclaw?timeout=1s")
	if err != nil {
		t.Fatalf("open unreachable db: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := GetIdentity(ctx, conn, 1); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("GetIdentity with db down = %v, want a non-ErrNotFound error", err)
	}
}
