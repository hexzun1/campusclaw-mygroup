package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"campusclaw/backend/internal/db"
)

// campus-tools is seeded by key, so a restart never overwrites the teacher's
// edits (base URL, connection state) and never creates a second row.
const (
	campusToolsSeedKey = "campus-tools"
	campusToolsName    = "campus-tools"
	campusToolsBaseURL = "http://mcp-demo:8090"
)

func ensureMCPServer(ctx context.Context, conn *sql.DB, classID, createdBy int, token string) error {
	if _, err := db.GetServerBySeedKey(ctx, conn, classID, campusToolsSeedKey); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrNotFound) {
		return err
	}

	_, err := db.CreateServer(ctx, conn, classID, campusToolsName, campusToolsBaseURL, token, createdBy, campusToolsSeedKey)
	if errors.Is(err, db.ErrDuplicateName) {
		log.Printf("seed: mcp server %q already exists in class %d outside the seed, keeping it", campusToolsName, classID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert seed mcp server: %w", err)
	}
	log.Printf("seed: created mcp server %q for class %d", campusToolsName, classID)
	return nil
}
