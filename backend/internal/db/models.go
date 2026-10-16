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

// SessionUser is the identity attached to an authenticated request. The token
// only proves who the caller is; every field here is read from the database on
// each request, so role and class changes take effect without a re-login.
// Business handlers read identity from this struct and nowhere else.
type SessionUser struct {
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
