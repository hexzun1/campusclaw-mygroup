package config

import (
	"strings"
	"testing"
)

func proxyStrings(cfg *Config) []string {
	out := make([]string, 0, len(cfg.TrustedProxyCIDRs))
	for _, p := range cfg.TrustedProxyCIDRs {
		out = append(out, p.String())
	}
	return out
}

// TestLoadTrustedProxyCIDRsDefaultEmpty covers the safe default: an unset (or
// blank) variable trusts no proxy, including loopback and private ranges.
func TestLoadTrustedProxyCIDRsDefaultEmpty(t *testing.T) {
	for _, value := range []string{"", " ", "\t\n"} {
		t.Run("value="+value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("TRUSTED_PROXY_CIDRS", value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() failed: %v", err)
			}
			if len(cfg.TrustedProxyCIDRs) != 0 {
				t.Fatalf("TrustedProxyCIDRs = %v, want empty", proxyStrings(cfg))
			}
		})
	}
}

// TestLoadTrustedProxyCIDRsNormalized checks the accepted formats and that
// entries are canonicalized (host bits masked, IPv4-mapped IPv6 reduced).
func TestLoadTrustedProxyCIDRsNormalized(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"single IPv4", "192.0.2.10", []string{"192.0.2.10/32"}},
		{"single IPv6", "2001:db8::1", []string{"2001:db8::1/128"}},
		{"CIDR", "10.0.0.0/8", []string{"10.0.0.0/8"}},
		{"host bits masked", "192.0.2.129/25", []string{"192.0.2.128/25"}},
		{"comma separated with spaces", " 192.0.2.1 , 2001:db8::/32 ", []string{"192.0.2.1/32", "2001:db8::/32"}},
		{"IPv4-mapped address", "::ffff:192.0.2.10", []string{"192.0.2.10/32"}},
		{"IPv4-mapped CIDR", "::ffff:192.0.2.0/120", []string{"192.0.2.0/24"}},
		{"almost all IPv4 is allowed", "0.0.0.0/1", []string{"0.0.0.0/1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("TRUSTED_PROXY_CIDRS", tc.raw)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() rejected %q: %v", tc.raw, err)
			}
			got := proxyStrings(cfg)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("TrustedProxyCIDRs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLoadTrustedProxyCIDRsRejectsInvalidAndGlobal covers the startup refusal
// required for malformed entries and any /0 (including a mapped IPv6 form that
// reduces to IPv4 /0). The error must name the setting.
func TestLoadTrustedProxyCIDRsRejectsInvalidAndGlobal(t *testing.T) {
	cases := []string{
		"not-an-ip",
		"192.0.2.1:8080",
		"192.0.2.0/33",
		"2001:db8::/129",
		"fe80::1%eth0",
		"192.0.2.1,",
		",192.0.2.1",
		",",
		"0.0.0.0/0",
		"::/0",
		"::ffff:0:0/96",
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("TRUSTED_PROXY_CIDRS", raw)

			cfg, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted TRUSTED_PROXY_CIDRS=%q (parsed %v), want error", raw, proxyStrings(cfg))
			}
			if !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
				t.Fatalf("error %q does not name TRUSTED_PROXY_CIDRS", err)
			}
		})
	}
}
