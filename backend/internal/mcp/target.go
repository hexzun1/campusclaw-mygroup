package mcp

import (
	"errors"
	"net"
	"strings"
)

// errForbiddenTarget is the single message for every blocked address, so a
// teacher learns the rule without learning the topology (spec: 工具服务器登记
// 与管理; design.md Decision 6).
var errForbiddenTarget = errors.New("服务器地址不允许指向本机、元数据或内部服务")

// forbiddenHostNames are the services of this deployment a registered server
// must never reach: the database, the api itself, the vector store, the web
// front end and the loopback alias.
var forbiddenHostNames = map[string]bool{
	"localhost": true,
	"db":        true,
	"api":       true,
	"qdrant":    true,
	"web":       true,
}

// checkHost rejects a host that points at the host itself, at link-local space
// (including the 169.254.169.254 metadata address) or at one of this
// deployment's own services. Private ranges stay allowed on purpose: mcp-demo
// lives in one (proposal: Non-goals).
//
// host may carry a port and IPv6 brackets; both are stripped first.
func checkHost(host string) error {
	name := normalizeHost(host)
	if name == "" || forbiddenHostNames[name] {
		return errForbiddenTarget
	}
	if ip := net.ParseIP(name); ip != nil && forbiddenIP(ip) {
		return errForbiddenTarget
	}
	return nil
}

// checkHostName is the name half of checkHost. Test clients skip the IP half so
// they can reach an httptest server, but the deployment's own service names stay
// forbidden for them too (design.md Decision 6).
func checkHostName(host string) error {
	name := normalizeHost(host)
	if name == "" || forbiddenHostNames[name] {
		return errForbiddenTarget
	}
	return nil
}

// normalizeHost lowercases a host and strips its port and IPv6 brackets.
func normalizeHost(host string) string {
	name := strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(name); err == nil {
		name = h
	}
	return strings.TrimPrefix(strings.TrimSuffix(name, "]"), "[")
}

// forbiddenIP reports whether an address is one the host must not dial:
// loopback (::ffff:127.0.0.1 included — net.IP.IsLoopback recognises the
// mapped form), link-local unicast or multicast, or the unspecified address.
func forbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
