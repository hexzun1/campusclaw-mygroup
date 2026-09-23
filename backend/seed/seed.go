// Package seed idempotently creates the sample classes, accounts and
// materials described in design.md Decision 6, so the app is usable right
// after `docker compose up --build` without manual setup.
package seed

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/bcrypt"

	"campusclaw/backend/internal/db"
)

type Config struct {
	TeacherAPassword  string
	StudentA1Password string
	StudentB1Password string
	UploadDir         string
}

const (
	classAName = "A班"
	classBName = "B班"

	materialATitle = "A 班教学材料示例"
	materialABody  = "# A 班材料\n\n这是 A 班的示例教学材料，用于验证班级隔离。"

	materialBTitle = "B 班教学材料示例"
	materialBBody  = "# B 班材料\n\n这是 B 班的示例教学材料，用于验证班级隔离。"
)

// Run seeds classes, users and sample materials. It is safe to call on every
// startup: existing rows (matched by unique username / class name) are left
// untouched, and no material seed is inserted if one with the same title
// already exists in that class.
func Run(ctx context.Context, conn *sql.DB, cfg Config) error {
	classA, err := ensureClass(ctx, conn, classAName)
	if err != nil {
		return fmt.Errorf("ensure class A: %w", err)
	}
	classB, err := ensureClass(ctx, conn, classBName)
	if err != nil {
		return fmt.Errorf("ensure class B: %w", err)
	}

	teacherA, err := ensureUser(ctx, conn, "teacher_a", cfg.TeacherAPassword, db.RoleTeacher, classA.ID)
	if err != nil {
		return fmt.Errorf("ensure teacher_a: %w", err)
	}
	if _, err := ensureUser(ctx, conn, "student_a1", cfg.StudentA1Password, db.RoleStudent, classA.ID); err != nil {
		return fmt.Errorf("ensure student_a1: %w", err)
	}
	studentB1, err := ensureUser(ctx, conn, "student_b1", cfg.StudentB1Password, db.RoleStudent, classB.ID)
	if err != nil {
		return fmt.Errorf("ensure student_b1: %w", err)
	}

	if err := ensureMaterial(ctx, conn, cfg.UploadDir, classA.ID, materialATitle, materialABody, teacherA.ID); err != nil {
		return fmt.Errorf("ensure material A: %w", err)
	}
	if err := ensureMaterial(ctx, conn, cfg.UploadDir, classB.ID, materialBTitle, materialBBody, studentB1.ID); err != nil {
		return fmt.Errorf("ensure material B: %w", err)
	}

	return nil
}

func ensureClass(ctx context.Context, conn *sql.DB, name string) (*db.Class, error) {
	c, err := db.GetClassByName(ctx, conn, name)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO classes (name) VALUES (?)`, name); err != nil {
		return nil, fmt.Errorf("insert class %s: %w", name, err)
	}
	log.Printf("seed: created class %s", name)
	return db.GetClassByName(ctx, conn, name)
}

func ensureUser(ctx context.Context, conn *sql.DB, username, password string, role db.Role, classID int) (*db.User, error) {
	u, err := db.GetUserByUsername(ctx, conn, username)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password for %s: %w", username, err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, class_id) VALUES (?, ?, ?, ?)`,
		username, string(hash), role, classID); err != nil {
		return nil, fmt.Errorf("insert user %s: %w", username, err)
	}
	log.Printf("seed: created user %s", username)
	return db.GetUserByUsername(ctx, conn, username)
}

func ensureMaterial(ctx context.Context, conn *sql.DB, uploadDir string, classID int, title, body string, uploadedBy int) error {
	var count int
	row := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM materials WHERE class_id = ? AND title = ?`, classID, title)
	if err := row.Scan(&count); err != nil {
		return fmt.Errorf("check existing material: %w", err)
	}
	if count > 0 {
		return nil
	}

	storedName, err := randomFilename(".md")
	if err != nil {
		return fmt.Errorf("generate stored name: %w", err)
	}

	classDir := filepath.Join(uploadDir, strconv.Itoa(classID))
	if err := os.MkdirAll(classDir, 0o755); err != nil {
		return fmt.Errorf("create class upload dir: %w", err)
	}
	fullPath := filepath.Join(classDir, storedName)
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write seed material file: %w", err)
	}

	if _, err := db.InsertMaterialWithKnowledge(ctx, conn, classID, title, storedName, title+".md", int64(len(body)), uploadedBy, body); err != nil {
		_ = os.Remove(fullPath)
		return fmt.Errorf("insert seed material: %w", err)
	}
	log.Printf("seed: created material %q for class %d", title, classID)
	return nil
}

func randomFilename(ext string) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ext, nil
}
