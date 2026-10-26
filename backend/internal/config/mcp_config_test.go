package config

import (
	"strings"
	"testing"
	"time"
)

func clearMCPEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"MCP_AUTH_TOKEN", "MCP_CONNECT_TIMEOUT", "MCP_CALL_TIMEOUT",
		"MCP_TOOL_STAGE_TIMEOUT", "MCP_MAX_RESPONSE_BYTES",
	} {
		t.Setenv(name, "")
	}
}

func TestLoadMCPDefaults(t *testing.T) {
	setRequiredEnv(t)
	clearMCPEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.MCPAuthToken != "" {
		t.Fatalf("MCPAuthToken = %q, want empty when unset", cfg.MCPAuthToken)
	}
	if cfg.MCPConnectTimeout != 5*time.Second {
		t.Fatalf("MCPConnectTimeout = %v, want 5s", cfg.MCPConnectTimeout)
	}
	if cfg.MCPCallTimeout != 10*time.Second {
		t.Fatalf("MCPCallTimeout = %v, want 10s", cfg.MCPCallTimeout)
	}
	if cfg.MCPToolStageTimeout != 20*time.Second {
		t.Fatalf("MCPToolStageTimeout = %v, want 20s", cfg.MCPToolStageTimeout)
	}
	if cfg.MCPMaxResponseBytes != 256*1024 {
		t.Fatalf("MCPMaxResponseBytes = %d, want 262144", cfg.MCPMaxResponseBytes)
	}
}

func TestLoadMCPOverrides(t *testing.T) {
	setRequiredEnv(t)
	clearMCPEnv(t)
	t.Setenv("MCP_CONNECT_TIMEOUT", "2s")
	t.Setenv("MCP_CALL_TIMEOUT", "3s")
	t.Setenv("MCP_TOOL_STAGE_TIMEOUT", "8s")
	t.Setenv("MCP_MAX_RESPONSE_BYTES", "1024")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.MCPConnectTimeout != 2*time.Second || cfg.MCPCallTimeout != 3*time.Second ||
		cfg.MCPToolStageTimeout != 8*time.Second || cfg.MCPMaxResponseBytes != 1024 {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

// TestLoadMCPInvalidSettings: a malformed MCP setting must abort startup rather
// than silently falling back to a default (design.md Decision 14).
func TestLoadMCPInvalidSettings(t *testing.T) {
	const token = "mcp-secret-value-do-not-echo"

	cases := []struct {
		name  string
		value string
	}{
		{"MCP_CONNECT_TIMEOUT", "not-a-duration"},
		{"MCP_CONNECT_TIMEOUT", "0s"},
		{"MCP_CALL_TIMEOUT", "-1s"},
		{"MCP_TOOL_STAGE_TIMEOUT", "20"},
		{"MCP_MAX_RESPONSE_BYTES", "big"},
		{"MCP_MAX_RESPONSE_BYTES", "0"},
		{"MCP_MAX_RESPONSE_BYTES", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			setRequiredEnv(t)
			clearMCPEnv(t)
			t.Setenv("MCP_AUTH_TOKEN", token)
			t.Setenv(tc.name, tc.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=%q, want error", tc.name, tc.value)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("error %q does not name %s", err, tc.name)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("error %q leaks the MCP token", err)
			}
		})
	}
}
