package auth

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// decodeSegment base64url-decodes one part of a compact JWS.
func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
	return out
}

func splitToken(t *testing.T, token string) []string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	return parts
}

func TestIssuePayloadIsExactlyTheFiveClaims(t *testing.T) {
	issuer := NewTokenIssuer(strings.Repeat("k", 32), 24*time.Hour)

	token, err := issuer.Issue(7)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := splitToken(t, token)

	if alg := decodeSegment(t, parts[0])["alg"]; alg != "HS256" {
		t.Fatalf("alg = %v, want HS256", alg)
	}

	payload := decodeSegment(t, parts[1])

	got := make([]string, 0, len(payload))
	for k := range payload {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"exp", "iat", "iss", "jti", "sub"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload keys = %v, want exactly %v", got, want)
	}

	if payload["sub"] != "7" {
		t.Errorf("sub = %v, want the decimal string \"7\"", payload["sub"])
	}
	if payload["iss"] != Issuer {
		t.Errorf("iss = %v, want %s", payload["iss"], Issuer)
	}

	iat, exp := payload["iat"].(float64), payload["exp"].(float64)
	if exp-iat != (24 * time.Hour).Seconds() {
		t.Errorf("exp - iat = %v, want %v", exp-iat, (24 * time.Hour).Seconds())
	}

	jti, ok := payload["jti"].(string)
	if !ok || len(jti) != 32 {
		t.Fatalf("jti = %v, want 32 hex characters", payload["jti"])
	}
}

func TestIssueUsesInjectedClockAndUniqueJTI(t *testing.T) {
	issuer := NewTokenIssuer(strings.Repeat("k", 32), 2*time.Hour)
	fixed := time.Date(2030, 5, 6, 7, 8, 9, 0, time.UTC)
	issuer.now = func() time.Time { return fixed }

	first, err := issuer.Issue(1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	second, err := issuer.Issue(1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	p1 := decodeSegment(t, splitToken(t, first)[1])
	p2 := decodeSegment(t, splitToken(t, second)[1])

	if p1["iat"] != float64(fixed.Unix()) {
		t.Errorf("iat = %v, want %v (injected clock)", p1["iat"], fixed.Unix())
	}
	if p1["exp"] != float64(fixed.Add(2*time.Hour).Unix()) {
		t.Errorf("exp = %v, want %v", p1["exp"], fixed.Add(2*time.Hour).Unix())
	}
	if p1["jti"] == p2["jti"] {
		t.Errorf("two issued tokens share jti %v", p1["jti"])
	}
}

func TestIssuePayloadHasNoCredentialMaterial(t *testing.T) {
	issuer := NewTokenIssuer(strings.Repeat("k", 32), time.Hour)

	token, err := issuer.Issue(1)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(splitToken(t, token)[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"password", "hash", "secret", "$2a$", "$2b$"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("payload contains %q: %s", forbidden, raw)
		}
	}
}
