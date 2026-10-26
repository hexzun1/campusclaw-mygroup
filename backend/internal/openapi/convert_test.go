package openapi

import (
	"encoding/json"
	"testing"

	"campusclaw/backend/docs"
)

// TestConvertGeneratedDocument converts the real generated document: every
// documented path must survive the 2.0 → 3.0 conversion.
func TestConvertGeneratedDocument(t *testing.T) {
	encoded, err := Convert(docs.SwaggerInfo.ReadDoc())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	var v3 struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(encoded, &v3); err != nil {
		t.Fatalf("decode converted document: %v", err)
	}
	if v3.OpenAPI != "3.0.3" && v3.OpenAPI != "3.0.0" {
		t.Errorf("openapi = %q, want a 3.0.x version", v3.OpenAPI)
	}
	for _, path := range []string{
		"/api/login",
		"/api/mcp/servers",
		"/api/mcp/servers/{id}/connect",
		"/api/mcp/servers/{id}/tools",
		"/api/mcp/servers/{id}/disconnect",
		"/api/mcp/calls",
		"/api/skills",
		"/api/conversations/{id}/stream",
	} {
		if _, ok := v3.Paths[path]; !ok {
			t.Errorf("converted document is missing %s", path)
		}
	}

	var swagger struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(docs.SwaggerInfo.ReadDoc()), &swagger); err != nil {
		t.Fatalf("decode generated document: %v", err)
	}
	if len(v3.Paths) != len(swagger.Paths) {
		t.Errorf("converted paths = %d, generated paths = %d", len(v3.Paths), len(swagger.Paths))
	}
}

func TestConvertRejectsMalformedInput(t *testing.T) {
	if _, err := Convert("not json"); err == nil {
		t.Fatal("Convert accepted malformed input")
	}
}
