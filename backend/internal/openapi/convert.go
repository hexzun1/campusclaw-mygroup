// Package openapi converts the generated Swagger 2.0 document to OpenAPI 3.0.
package openapi

import (
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
)

// Convert parses the generated Swagger 2.0 JSON and returns the OpenAPI 3.0
// encoding. Callers convert once at startup and cache the result, so the
// served document always matches the generated one (spec: API 文档).
func Convert(swaggerJSON string) ([]byte, error) {
	var doc openapi2.T
	if err := json.Unmarshal([]byte(swaggerJSON), &doc); err != nil {
		return nil, fmt.Errorf("parse swagger 2.0: %w", err)
	}
	v3, err := openapi2conv.ToV3(&doc)
	if err != nil {
		return nil, fmt.Errorf("convert to openapi 3.0: %w", err)
	}
	encoded, err := json.Marshal(v3)
	if err != nil {
		return nil, fmt.Errorf("encode openapi 3.0: %w", err)
	}
	return encoded, nil
}
