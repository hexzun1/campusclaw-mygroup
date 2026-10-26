package mcp

import (
	"strings"
	"testing"
)

// TestCheckHostTable covers spec 「工具服务器登记与管理」Scenario 被禁止的地址
// plus the IPv4-mapped form the resolver may hand back.
func TestCheckHostTable(t *testing.T) {
	forbidden := []string{
		"localhost",
		"localhost:8090",
		"LOCALHOST:8090",
		"127.0.0.1",
		"127.0.0.1:8090",
		"127.5.6.7",
		"[::1]",
		"[::1]:8090",
		"::1",
		"169.254.169.254",
		"169.254.169.254:80",
		"0.0.0.0",
		"0.0.0.0:8090",
		"::",
		"[::]",
		"db",
		"db:3306",
		"DB:3306",
		"api",
		"API:8081",
		"qdrant",
		"qdrant:6333",
		"web",
		"[::ffff:127.0.0.1]",
		"::ffff:127.0.0.1",
		"[fe80::1]",
		"fe80::1",
		"",
	}
	for _, host := range forbidden {
		if err := checkHost(host); err == nil {
			t.Errorf("checkHost(%q) = nil, want it rejected", host)
		}
	}

	allowed := []string{
		"mcp-demo",
		"mcp-demo:8090",
		"10.0.0.5",
		"10.0.0.5:8090",
		"192.168.1.10:8090",
		"172.16.0.9",
		"example.com",
		"example.com:443",
		"tools.example.com",
		"mcp.localhost.example.com",
		"notdb",
	}
	for _, host := range allowed {
		if err := checkHost(host); err != nil {
			t.Errorf("checkHost(%q) = %v, want it allowed", host, err)
		}
	}
}

// TestValidateBaseURLRejectsForbiddenTargets ties checkHost to the 400 path:
// each address of the spec scenario is refused with the address message.
func TestValidateBaseURLRejectsForbiddenTargets(t *testing.T) {
	rejected := []string{
		"http://localhost:8090",
		"http://127.0.0.1:8090",
		"http://[::1]:8090",
		"http://169.254.169.254",
		"http://0.0.0.0:8090",
		"http://db:3306",
		"http://API:8081",
		"http://qdrant:6333",
		"http://web",
		"http://[::ffff:127.0.0.1]:8090",
	}
	for _, raw := range rejected {
		err := validateBaseURL(raw)
		if err == nil {
			t.Errorf("validateBaseURL(%q) = nil, want rejected", raw)
			continue
		}
		if !strings.Contains(err.Error(), "不允许") {
			t.Errorf("validateBaseURL(%q) = %q, want the address message", raw, err)
		}
	}

	accepted := []string{
		"http://mcp-demo:8090",
		"http://10.0.0.5:8090",
		"https://tools.example.com",
	}
	for _, raw := range accepted {
		if err := validateBaseURL(raw); err != nil {
			t.Errorf("validateBaseURL(%q) = %v, want accepted", raw, err)
		}
	}
}
