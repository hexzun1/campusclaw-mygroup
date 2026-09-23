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

type Session struct {
	ID        string
	UserID    int
	ExpiresAt time.Time
}

// SessionUser is the joined view of a session with its owning user, used by
// the session middleware to populate request context.
type SessionUser struct {
	SessionID string
	UserID    int
	Username  string
	Role      Role
	ClassID   int
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
