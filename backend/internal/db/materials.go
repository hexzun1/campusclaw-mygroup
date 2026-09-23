package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ListMaterials returns materials for classID only, optionally filtered by a
// case-insensitive title substring q. classID MUST come from the caller's
// session, never from client-supplied input.
func ListMaterials(ctx context.Context, conn *sql.DB, classID int, q string) ([]Material, error) {
	query := `SELECT id, class_id, title, stored_name, original_name, size_bytes, uploaded_by, created_at
		FROM materials WHERE class_id = ?`
	args := []any{classID}
	if q != "" {
		query += " AND title LIKE ?"
		args = append(args, "%"+q+"%")
	}
	query += " ORDER BY created_at DESC"

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list materials: %w", err)
	}
	defer rows.Close()

	var out []Material
	for rows.Next() {
		var m Material
		if err := rows.Scan(&m.ID, &m.ClassID, &m.Title, &m.StoredName, &m.OriginalName, &m.SizeBytes, &m.UploadedBy, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan material: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ErrNotFound is returned when a row does not exist. Callers performing
// class-isolation checks MUST treat this identically to a cross-class hit
// (see design.md Decision 4): both return 404 with the same response body.
var ErrNotFound = errors.New("not found")

// GetMaterialByID fetches a material by id only, with no class filter in the
// SQL. Callers MUST compare the returned ClassID against the caller's session
// class themselves and return 404 for both "not found" and "wrong class".
func GetMaterialByID(ctx context.Context, conn *sql.DB, id int) (*Material, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, class_id, title, stored_name, original_name, size_bytes, uploaded_by, created_at
		FROM materials WHERE id = ?`, id)

	var m Material
	if err := row.Scan(&m.ID, &m.ClassID, &m.Title, &m.StoredName, &m.OriginalName, &m.SizeBytes, &m.UploadedBy, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get material: %w", err)
	}
	return &m, nil
}

// GetKnowledgeEntryByMaterialID fetches the knowledge entry body for a
// material. Class-isolation checks are the caller's responsibility.
func GetKnowledgeEntryByMaterialID(ctx context.Context, conn *sql.DB, materialID int) (*KnowledgeEntry, error) {
	row := conn.QueryRowContext(ctx, `SELECT id, material_id, class_id, body_text, created_at
		FROM knowledge_entries WHERE material_id = ?`, materialID)

	var k KnowledgeEntry
	if err := row.Scan(&k.ID, &k.MaterialID, &k.ClassID, &k.BodyText, &k.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get knowledge entry: %w", err)
	}
	return &k, nil
}

// InsertMaterialWithKnowledge writes materials and knowledge_entries in one
// transaction. classID MUST come from the caller's session. On any failure,
// the transaction is rolled back and no rows are left behind.
func InsertMaterialWithKnowledge(ctx context.Context, conn *sql.DB, classID int, title, storedName, originalName string, sizeBytes int64, uploadedBy int, bodyText string) (materialID int, err error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.ExecContext(ctx, `INSERT INTO materials (class_id, title, stored_name, original_name, size_bytes, uploaded_by)
		VALUES (?, ?, ?, ?, ?, ?)`, classID, title, storedName, originalName, sizeBytes, uploadedBy)
	if err != nil {
		return 0, fmt.Errorf("insert material: %w", err)
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("material last insert id: %w", err)
	}
	materialID = int(id64)

	if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_entries (material_id, class_id, body_text)
		VALUES (?, ?, ?)`, materialID, classID, bodyText); err != nil {
		return 0, fmt.Errorf("insert knowledge entry: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return materialID, nil
}
