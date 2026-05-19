package host

import (
	"net"
	"net/http"
)

// remoteIP extracts the peer IP from an http.Request, matching what
// C# `HttpContext.Connection.RemoteIpAddress` returns. Returns nil if
// the address cannot be parsed. IPv4-mapped-IPv6 addresses are
// normalised to their IPv4 form (parity with C# `MapToIPv4` at
// `RequestExtensions.cs:92-95`).
func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// isLocalAddress mirrors C# `IPAddressExtensions.IsLocalAddress`
// (`src/NzbDrone.Common/Extensions/IpAddressExtensions.cs:8-37`):
// loopback, IPv4 private (10/8, 172.16/12, 192.168/16) + link-local
// (169.254/16), and IPv6 link-local / unique-local / site-local.
func isLocalAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 10:
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		case v4[0] == 169 && v4[1] == 254:
			return true
		}
		return false
	}
	// IPv6: link-local (fe80::/10), unique-local (fc00::/7),
	// site-local (fec0::/10 — deprecated but C# still accepts it).
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return true
	}
	if len(ip) == net.IPv6len && ip[0] == 0xfe && (ip[1]&0xc0) == 0xc0 {
		return true
	}
	return false
}

// isCgnatAddress mirrors C#
// `IPAddressExtensions.IsCgnatIpAddress` — IPv4 100.64.0.0/10
// (RFC 6598, "Carrier-Grade NAT" range). Returns false for IPv6.
func isCgnatAddress(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}
