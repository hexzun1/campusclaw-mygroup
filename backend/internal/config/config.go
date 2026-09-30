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
	JWTSecret string
	JWTTTL    time.Duration

	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string

	UploadDir      string
	MaxUploadBytes int64

	LoginMaxFailures int
	LoginLockSeconds int

	SeedTeacherAPassword  string
	SeedStudentA1Password string
	SeedStudentB1Password string

	APIPort string
	WebPort string

	// Vector retrieval (iteration 2, design.md Decision 4 and Decision 10).
	// Declared required: a missing gateway or vector-store setting must stop
	// the process rather than silently disabling vector search.
	QdrantURL    string
	QdrantAPIKey string

	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingModel     string
	EmbeddingDim       int
	EmbeddingBatchSize int

	ChatBaseURL string
	ChatAPIKey  string
	ChatModel   string

	GatewayTimeout time.Duration
	IndexTimeout   time.Duration
}

const (
	// MinJWTSecretLength is the shortest accepted JWT_SECRET. A shorter value
	// is rejected at startup (spec: 口令与密钥安全).
	MinJWTSecretLength = 32

	defaultJWTTTL           = 24 * time.Hour
	defaultDBPort           = "3306"
	defaultMaxUploadBytes   = 2 * 1024 * 1024
	defaultLoginMaxFailures = 5
	defaultLoginLockSeconds = 300
	defaultAPIPort          = "8081"
	defaultWebPort          = "8080"

	defaultEmbeddingBatchSize = 32
	defaultGatewayTimeoutSec  = 30
	defaultIndexTimeoutSec    = 120
)

// required lists the environment variables that MUST be set. Missing any of
// them causes Load to return an error naming every missing variable.
var required = []string{
	"JWT_SECRET",
	"DB_HOST",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"UPLOAD_DIR",
	"SEED_TEACHER_A_PASSWORD",
	"SEED_STUDENT_A1_PASSWORD",
	"SEED_STUDENT_B1_PASSWORD",
	"QDRANT_URL",
	"EMBEDDING_BASE_URL",
	"EMBEDDING_API_KEY",
	"EMBEDDING_MODEL",
	"EMBEDDING_DIM",
	"CHAT_BASE_URL",
	"CHAT_API_KEY",
	"CHAT_MODEL",
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

	// EMBEDDING_DIM decides the vector collection size, so a malformed value is
	// fatal instead of silently falling back to a default.
	embeddingDim, err := positiveInt("EMBEDDING_DIM")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		JWTSecret: os.Getenv("JWT_SECRET"),
		JWTTTL:    durationOrDefault("JWT_TTL", defaultJWTTTL),

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

		QdrantURL:    os.Getenv("QDRANT_URL"),
		QdrantAPIKey: os.Getenv("QDRANT_API_KEY"),

		EmbeddingBaseURL:   os.Getenv("EMBEDDING_BASE_URL"),
		EmbeddingAPIKey:    os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:     os.Getenv("EMBEDDING_MODEL"),
		EmbeddingDim:       embeddingDim,
		EmbeddingBatchSize: intOrDefault("EMBEDDING_BATCH_SIZE", defaultEmbeddingBatchSize),

		ChatBaseURL: os.Getenv("CHAT_BASE_URL"),
		ChatAPIKey:  os.Getenv("CHAT_API_KEY"),
		ChatModel:   os.Getenv("CHAT_MODEL"),

		GatewayTimeout: secondsOrDefault("GATEWAY_TIMEOUT_SECONDS", defaultGatewayTimeoutSec),
		IndexTimeout:   secondsOrDefault("INDEX_TIMEOUT_SECONDS", defaultIndexTimeoutSec),
	}

	// The secret is never echoed in the error: only the variable name, the
	// rule it violated and the observed length are reported.
	if len(cfg.JWTSecret) < MinJWTSecretLength {
		return nil, fmt.Errorf(
			"JWT_SECRET is invalid: must be at least %d characters (got %d)",
			MinJWTSecretLength, len(cfg.JWTSecret))
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

// secondsOrDefault reads a whole number of seconds. Only optional settings use
// it; required settings are validated by positiveInt instead.
func secondsOrDefault(name string, def int) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return time.Duration(def) * time.Second
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return time.Duration(def) * time.Second
	}
	return time.Duration(n) * time.Second
}

// positiveInt reads an environment variable that must hold a positive integer.
// The value is never echoed back: only the variable name and the rule.
func positiveInt(name string) (int, error) {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s is invalid: must be a positive integer", name)
	}
	return n, nil
}
