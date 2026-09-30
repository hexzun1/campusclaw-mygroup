package config

import (
	"strings"
	"testing"
	"time"
)

// requiredExceptJWT lists every required variable other than JWT_SECRET, which
// each test sets explicitly.
var requiredExceptJWT = []string{
	"DB_HOST",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"UPLOAD_DIR",
	"SEED_TEACHER_A_PASSWORD",
	"SEED_STUDENT_A1_PASSWORD",
	"SEED_STUDENT_B1_PASSWORD",
}

// setRequiredEnv sets every required variable that is unrelated to the JWT
// secret, so a test can isolate the JWT_SECRET outcome.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	for _, name := range requiredExceptJWT {
		t.Setenv(name, "test-value")
	}
}

func TestLoadMissingJWTSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded without JWT_SECRET, want error")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("error %q does not name JWT_SECRET", err)
	}
	if strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Fatalf("error %q still references SESSION_SECRET", err)
	}
}

func TestLoadJWTSecretTooShort(t *testing.T) {
	setRequiredEnv(t)
	secret := strings.Repeat("s", MinJWTSecretLength-1)
	t.Setenv("JWT_SECRET", secret)

	_, err := Load()
	if err == nil {
		t.Fatalf("Load() accepted a %d-character JWT_SECRET, want error", len(secret))
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("error %q does not name JWT_SECRET", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error %q leaks the JWT_SECRET value", err)
	}
}

func TestLoadJWTSecretMinLengthAccepted(t *testing.T) {
	setRequiredEnv(t)
	secret := strings.Repeat("s", MinJWTSecretLength)
	t.Setenv("JWT_SECRET", secret)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() rejected a %d-character JWT_SECRET: %v", MinJWTSecretLength, err)
	}
	if cfg.JWTSecret != secret {
		t.Fatalf("JWTSecret = %q, want the configured value", cfg.JWTSecret)
	}
}

func TestLoadJWTTTLDefault(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", strings.Repeat("s", MinJWTSecretLength))
	t.Setenv("JWT_TTL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.JWTTTL != 24*time.Hour {
		t.Fatalf("JWTTTL = %v, want 24h", cfg.JWTTTL)
	}
}

func TestLoadJWTTTLOverride(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", strings.Repeat("s", MinJWTSecretLength))
	t.Setenv("JWT_TTL", "2h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.JWTTTL != 2*time.Hour {
		t.Fatalf("JWTTTL = %v, want 2h", cfg.JWTTTL)
	}
}
