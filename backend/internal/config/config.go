package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration read from environment variables,
// per design.md Decision 7. Required variables cause the process to exit
// non-zero with the missing variable names when absent.
type Config struct {
	SessionSecret string
	SessionTTL    time.Duration

	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string

	UploadDir      string
	MaxUploadBytes int64

	LoginMaxFailures int
	LoginLockSeconds int

	SeedTeacherAPassword   string
	SeedStudentA1Password  string
	SeedStudentB1Password  string

	APIPort string
	WebPort string
}

const (
	defaultSessionTTL        = 24 * time.Hour
	defaultDBPort            = "3306"
	defaultMaxUploadBytes    = 2 * 1024 * 1024
	defaultLoginMaxFailures  = 5
	defaultLoginLockSeconds  = 300
	defaultAPIPort           = "8081"
	defaultWebPort           = "8080"
)

// required lists the environment variables that MUST be set. Missing any of
// them causes Load to return an error naming every missing variable.
var required = []string{
	"SESSION_SECRET",
	"DB_HOST",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"UPLOAD_DIR",
	"SEED_TEACHER_A_PASSWORD",
	"SEED_STUDENT_A1_PASSWORD",
	"SEED_STUDENT_B1_PASSWORD",
}

// Load reads configuration from the environment. It returns an error listing
// every missing required variable name; callers MUST exit non-zero on error
// and MUST NOT fall back to built-in defaults for required variables.
func Load() (*Config, error) {
	var missing []string
	for _, name := range required {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	cfg := &Config{
		SessionSecret: os.Getenv("SESSION_SECRET"),
		SessionTTL:    durationOrDefault("SESSION_TTL", defaultSessionTTL),

		DBHost:     os.Getenv("DB_HOST"),
		DBPort:     stringOrDefault("DB_PORT", defaultDBPort),
		DBName:     os.Getenv("DB_NAME"),
		DBUser:     os.Getenv("DB_USER"),
		DBPassword: os.Getenv("DB_PASSWORD"),

		UploadDir:      os.Getenv("UPLOAD_DIR"),
		MaxUploadBytes: int64OrDefault("MAX_UPLOAD_BYTES", defaultMaxUploadBytes),

		LoginMaxFailures: intOrDefault("LOGIN_MAX_FAILURES", defaultLoginMaxFailures),
		LoginLockSeconds: intOrDefault("LOGIN_LOCK_SECONDS", defaultLoginLockSeconds),

		SeedTeacherAPassword:  os.Getenv("SEED_TEACHER_A_PASSWORD"),
		SeedStudentA1Password: os.Getenv("SEED_STUDENT_A1_PASSWORD"),
		SeedStudentB1Password: os.Getenv("SEED_STUDENT_B1_PASSWORD"),

		APIPort: stringOrDefault("API_PORT", defaultAPIPort),
		WebPort: stringOrDefault("WEB_PORT", defaultWebPort),
	}

	return cfg, nil
}

func stringOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func intOrDefault(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func int64OrDefault(name string, def int64) int64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func durationOrDefault(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
