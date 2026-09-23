package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Open connects to MySQL and retries for a while, since the db container can
// take tens of seconds to become ready on first start (see design.md Risks).
func Open(host, port, name, user, password string) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4", user, password, host, port, name)

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	const maxAttempts = 30
	var pingErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pingErr = sqlDB.Ping()
		if pingErr == nil {
			return sqlDB, nil
		}
		log.Printf("db not ready yet (attempt %d/%d): %v", attempt, maxAttempts, pingErr)
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("db ping failed after %d attempts: %w", maxAttempts, pingErr)
}
