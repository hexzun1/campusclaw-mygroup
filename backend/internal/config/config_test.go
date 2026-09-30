package config

import (
	"strings"
	"testing"
	"time"
)

// allRequiredExceptJWT lists every required variable other than JWT_SECRET,
// which setRequiredEnv sets explicitly.
var allRequiredExceptJWT = []string{
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

// validJWTSecret is a placeholder long enough to pass MinJWTSecretLength.
const validJWTSecret = "0123456789abcdef0123456789abcdef"

// setRequiredEnv sets every required variable so a test can isolate the
// outcome of one of them by clearing or overriding it afterwards.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", validJWTSecret)
	for _, name := range allRequiredExceptJWT {
		t.Setenv(name, "test-value")
	}
	t.Setenv("EMBEDDING_DIM", "1024")
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

// TestLoadMissingEachRequiredVariable clears one required variable at a time and
// asserts the error names it, so a renamed or dropped entry in the required
// list is caught (spec: 外部服务凭据与访问边界 — 缺少必需配置时启动失败).
func TestLoadMissingEachRequiredVariable(t *testing.T) {
	for _, name := range append([]string{"JWT_SECRET"}, allRequiredExceptJWT...) {
		t.Run(name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(name, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded without %s, want error", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("error %q does not name %s", err, name)
			}
		})
	}
}

func TestLoadEmbeddingDimNotANumber(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("EMBEDDING_DIM", "not-a-number")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted a non-numeric EMBEDDING_DIM, want error")
	}
	if !strings.Contains(err.Error(), "EMBEDDING_DIM") {
		t.Fatalf("error %q does not name EMBEDDING_DIM", err)
	}
}

func TestLoadEmbeddingDimNotPositive(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.5", " "} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("EMBEDDING_DIM", value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted EMBEDDING_DIM=%q, want error", value)
			}
			if !strings.Contains(err.Error(), "EMBEDDING_DIM") {
				t.Fatalf("error %q does not name EMBEDDING_DIM", err)
			}
		})
	}
}

// TestLoadAllRequiredProvided checks the happy path plus the documented
// defaults for the optional iteration-2 settings.
func TestLoadAllRequiredProvided(t *testing.T) {
	setRequiredEnv(t)
	for _, name := range []string{
		"QDRANT_API_KEY", "EMBEDDING_BATCH_SIZE",
		"GATEWAY_TIMEOUT_SECONDS", "INDEX_TIMEOUT_SECONDS",
	} {
		t.Setenv(name, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed with every required variable set: %v", err)
	}
	if cfg.EmbeddingDim != 1024 {
		t.Fatalf("EmbeddingDim = %d, want 1024", cfg.EmbeddingDim)
	}
	if cfg.EmbeddingBatchSize != 32 {
		t.Fatalf("EmbeddingBatchSize = %d, want 32", cfg.EmbeddingBatchSize)
	}
	if cfg.GatewayTimeout != 30*time.Second {
		t.Fatalf("GatewayTimeout = %v, want 30s", cfg.GatewayTimeout)
	}
	if cfg.IndexTimeout != 120*time.Second {
		t.Fatalf("IndexTimeout = %v, want 120s", cfg.IndexTimeout)
	}
	if cfg.QdrantAPIKey != "" {
		t.Fatalf("QdrantAPIKey = %q, want empty when unset", cfg.QdrantAPIKey)
	}
	if cfg.QdrantURL != "test-value" {
		t.Fatalf("QdrantURL = %q, want the configured value", cfg.QdrantURL)
	}
}

func TestLoadOptionalOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("QDRANT_API_KEY", "qdrant-secret")
	t.Setenv("EMBEDDING_BATCH_SIZE", "8")
	t.Setenv("GATEWAY_TIMEOUT_SECONDS", "5")
	t.Setenv("INDEX_TIMEOUT_SECONDS", "45")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.QdrantAPIKey != "qdrant-secret" {
		t.Fatalf("QdrantAPIKey = %q, want the configured value", cfg.QdrantAPIKey)
	}
	if cfg.EmbeddingBatchSize != 8 {
		t.Fatalf("EmbeddingBatchSize = %d, want 8", cfg.EmbeddingBatchSize)
	}
	if cfg.GatewayTimeout != 5*time.Second {
		t.Fatalf("GatewayTimeout = %v, want 5s", cfg.GatewayTimeout)
	}
	if cfg.IndexTimeout != 45*time.Second {
		t.Fatalf("IndexTimeout = %v, want 45s", cfg.IndexTimeout)
	}
}
