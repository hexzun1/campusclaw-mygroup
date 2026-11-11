package auth

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ErrInvalidClientAddr means the server could not determine a trustworthy
// client address for this login request: the direct peer is not a literal IP,
// or a trusted proxy did not supply exactly one valid X-Real-IP literal.
// Callers MUST answer 400 before consulting the limiter or any password.
var ErrInvalidClientAddr = errors.New("invalid client address")

// ClientIP resolves the login rate-limit identity from the request, following
// the server-side trust boundary in design.md D2:
//
//  1. The direct TCP peer is parsed from RemoteAddr (a TCP port is ignored)
//     and canonicalized. If it is not a literal IP, the request is rejected.
//  2. If the peer is not in trusted, its own IP is used and every forwarding
//     header (X-Real-IP, X-Forwarded-For, Forwarded) is ignored.
//  3. If the peer is in trusted, exactly one X-Real-IP header value is
//     required. After trimming it MUST be a single IPv4/IPv6 literal with no
//     port, zone, comma or list; anything else is rejected rather than
//     falling back to the shared proxy address.
//
// The returned address is unmapped and canonical, so equivalent IPv6
// spellings and an IPv4-mapped IPv6 form share one bucket.
func ClientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, error) {
	peer, err := parseRemoteAddr(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, ErrInvalidClientAddr
	}
	if !isTrustedPeer(peer, trusted) {
		return peer, nil
	}

	values := r.Header.Values("X-Real-IP")
	if len(values) != 1 {
		return netip.Addr{}, ErrInvalidClientAddr
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" {
		return netip.Addr{}, ErrInvalidClientAddr
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, ErrInvalidClientAddr
	}
	return addr.Unmap(), nil
}

// isTrustedPeer reports whether the direct peer matches the explicitly
// configured trust list. An empty list trusts nothing.
func isTrustedPeer(peer netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(peer) {
			return true
		}
	}
	return false
}

// parseRemoteAddr extracts the IP from an http.Request.RemoteAddr value and
// canonicalizes it. With a port, the host part is used; without one, the whole
// value must still be a bare IP literal. Hostnames are never resolved.
func parseRemoteAddr(remote string) (netip.Addr, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return netip.Addr{}, ErrInvalidClientAddr
	}

	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}

	addr, err := netip.ParseAddr(host)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, ErrInvalidClientAddr
	}
	return addr.Unmap(), nil
}
