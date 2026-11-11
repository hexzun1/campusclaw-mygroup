package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func newClientIPRequest(remote string, headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = remote
	for name, values := range headers {
		for _, value := range values {
			r.Header.Add(name, value)
		}
	}
	return r
}

func mustParsePrefixForTest(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", s, err)
	}
	return p
}

// TestClientIPDirectPeerIgnoresForwardingHeaders covers an untrusted direct
// client: its RemoteAddr is the only source, and forged headers must neither
// choose another bucket nor make the server trust an unlisted proxy. Private
// and loopback peers are not trusted implicitly.
func TestClientIPDirectPeerIgnoresForwardingHeaders(t *testing.T) {
	spoofed := map[string][]string{
		"X-Real-IP":       {"203.0.113.9"},
		"X-Forwarded-For": {"203.0.113.9, 198.51.100.20"},
		"Forwarded":       {"for=203.0.113.9"},
	}

	cases := []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		headers map[string][]string
		want    string
	}{
		{"public direct", nil, "198.51.100.10:4321", spoofed, "198.51.100.10"},
		{"private direct is not trusted implicitly", nil, "10.1.2.3:5555", spoofed, "10.1.2.3"},
		{"loopback is not trusted implicitly", nil, "127.0.0.1:5555", spoofed, "127.0.0.1"},
		{"trusted list does not match peer", []netip.Prefix{mustParsePrefixForTest(t, "192.0.2.0/24")}, "198.51.100.10:1", spoofed, "198.51.100.10"},
		{"bare IPv4 remote", nil, "198.51.100.10", spoofed, "198.51.100.10"},
		{"bare IPv6 remote", nil, "2001:db8::1", spoofed, "2001:db8::1"},
		{"IPv6 remote with port", nil, "[2001:db8::1]:443", spoofed, "2001:db8::1"},
		{"mapped remote is unmapped", nil, "[::ffff:198.51.100.10]:443", spoofed, "198.51.100.10"},
		{"different source ports are equal", nil, "198.51.100.10:9999", spoofed, "198.51.100.10"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClientIP(newClientIPRequest(tc.remote, tc.headers), tc.trusted)
			if err != nil {
				t.Fatalf("ClientIP: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestClientIPTrustedProxyUsesSingleXRealIP covers a matching direct proxy:
// the single valid X-Real-IP literal is canonicalized; X-Forwarded-For and
// Forwarded never participate.
func TestClientIPTrustedProxyUsesSingleXRealIP(t *testing.T) {
	trusted := []netip.Prefix{mustParsePrefixForTest(t, "192.0.2.0/24"), mustParsePrefixForTest(t, "2001:db8:ffff::/48")}

	cases := []struct {
		name   string
		remote string
		header []string
		want   string
	}{
		{"IPv4", "192.0.2.7:1234", []string{"198.51.100.10"}, "198.51.100.10"},
		{"trimmed IPv6", "192.0.2.7:1234", []string{" \t2001:db8::1\n"}, "2001:db8::1"},
		{"compressed and expanded are equal", "192.0.2.7:1234", []string{"2001:0db8:0000:0000:0000:0000:0000:0001"}, "2001:db8::1"},
		{"mapped IPv4 is unmapped", "192.0.2.7:1234", []string{"::ffff:198.51.100.10"}, "198.51.100.10"},
		{"IPv6 proxy peer", "[2001:db8:ffff::1]:1234", []string{"198.51.100.10"}, "198.51.100.10"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string][]string{
				"X-Real-IP":       tc.header,
				"X-Forwarded-For": {"203.0.113.99, 203.0.113.98"},
				"Forwarded":       {"for=203.0.113.97"},
			}
			got, err := ClientIP(newClientIPRequest(tc.remote, headers), trusted)
			if err != nil {
				t.Fatalf("ClientIP: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestClientIPTrustedProxyRejectsInvalidXRealIP covers every malformed header
// shape: the proxy's own address must never become the fallback bucket.
func TestClientIPTrustedProxyRejectsInvalidXRealIP(t *testing.T) {
	trusted := []netip.Prefix{mustParsePrefixForTest(t, "192.0.2.0/24")}

	cases := []struct {
		name   string
		header []string
	}{
		{"missing", nil},
		{"empty", []string{""}},
		{"whitespace only", []string{"   "}},
		{"duplicate headers", []string{"198.51.100.10", "198.51.100.11"}},
		{"comma separated list", []string{"198.51.100.10, 198.51.100.11"}},
		{"address with port", []string{"198.51.100.10:1234"}},
		{"IPv6 zone", []string{"fe80::1%eth0"}},
		{"CIDR instead of address", []string{"198.51.100.0/24"}},
		{"bracketed IPv6", []string{"[2001:db8::1]"}},
		{"arbitrary text", []string{"not-an-address"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string][]string{"X-Real-IP": tc.header}
			if tc.header == nil {
				headers = nil
			}
			_, err := ClientIP(newClientIPRequest("192.0.2.7:1234", headers), trusted)
			if !errors.Is(err, ErrInvalidClientAddr) {
				t.Fatalf("ClientIP error = %v, want ErrInvalidClientAddr", err)
			}
		})
	}
}

// TestClientIPRejectsUnparseableDirectPeer ensures an arbitrary RemoteAddr
// string cannot become a bucket, and a forged header cannot rescue it.
func TestClientIPRejectsUnparseableDirectPeer(t *testing.T) {
	forged := map[string][]string{"X-Real-IP": {"198.51.100.10"}}
	for _, remote := range []string{"", "not-an-ip", "example.com:443", "192.0.2.1/24", "[2001:db8::1]", "fe80::1%eth0:443"} {
		t.Run(remote, func(t *testing.T) {
			_, err := ClientIP(newClientIPRequest(remote, forged), nil)
			if !errors.Is(err, ErrInvalidClientAddr) {
				t.Fatalf("ClientIP(%q) error = %v, want ErrInvalidClientAddr", remote, err)
			}
		})
	}
}
