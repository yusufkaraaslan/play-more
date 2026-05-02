package middleware

import (
	"net"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

var trustedProxyList []string

// SetTrustedProxies stores the parsed trusted-proxy CIDRs for RealClientIP.
func SetTrustedProxies(proxies []string) {
	trustedProxyList = proxies
}

// RealClientIP returns the client's actual IP address, safe from X-Forwarded-For
// spoofing.  When no trusted proxies are configured it uses the TCP connection's
// RemoteAddr directly.  When trusted proxies ARE configured it prefers
// X-Real-IP (set by the immediate upstream proxy) and falls back to walking
// X-Forwarded-For from the RIGHT — the first non-trusted IP is the real client.
//
// This fixes the vulnerability in Gin's c.ClientIP() which returns the
// left-most XFF element.  That element is under attacker control whenever a
// client connects through a proxy that simply appends to XFF rather than
// replacing it (the default behaviour of nginx, AWS ALB, etc.).
func RealClientIP(c *gin.Context) string {
	// No trusted proxies → trust the TCP connection directly.
	if len(trustedProxyList) == 0 {
		return stripPort(c.Request.RemoteAddr)
	}

	// Prefer X-Real-IP (set by the immediate upstream proxy).
	if xri := strings.TrimSpace(c.Request.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}

	// Walk X-Forwarded-For from the right (closest to server) outward.
	xff := c.Request.Header.Get("X-Forwarded-For")
	if xff == "" {
		return stripPort(c.Request.RemoteAddr)
	}

	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		if !isTrustedProxyAddr(candidate) {
			return candidate
		}
	}

	// Every IP in the chain was a trusted proxy — fall back to RemoteAddr.
	return stripPort(c.Request.RemoteAddr)
}

func stripPort(addr string) string {
	ip, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return ip
}

func isTrustedProxyAddr(addr string) bool {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	for _, cidr := range trustedProxyList {
		if cidr == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			// Not a CIDR — try plain IP comparison.
			proxyIP, err := netip.ParseAddr(cidr)
			if err != nil {
				continue
			}
			if ip == proxyIP {
				return true
			}
			continue
		}
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
