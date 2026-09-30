package db

import "time"

type Role string

const (
	RoleTeacher Role = "teacher"
	RoleStudent Role = "student"
)

type User struct {
	ID           int
	Username     string
	PasswordHash string
	Role         Role
	ClassID      int
}

type Class struct {
	ID   int
	Name string
}

// SessionUser is the identity attached to an authenticated request. It is
// built from the verified token's claims (the `sessions` table is no longer
// read) and is the only source of role and class for business handlers.
type SessionUser struct {
	UserID   int
	Username string
	Role     Role
	ClassID  int
	// ClassName is not a token claim: it stays empty unless a handler resolves
	// it from class_id.
	ClassName string
}

type Material struct {
	ID           int
	ClassID      int
	Title        string
	StoredName   string
	OriginalName string
	SizeBytes    int64
	UploadedBy   int
	CreatedAt    time.Time
}

type KnowledgeEntry struct {
	ID         int
	MaterialID int
	ClassID    int
	BodyText   string
	CreatedAt  time.Time
}
