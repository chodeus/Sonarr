package host

import (
	"net"
	"net/http"
	"testing"
)

func TestIsLocalAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"10.0.0.5", true},
		{"172.15.0.1", false},    // just below the class-B private range
		{"172.16.0.1", true},     // start of class-B private range
		{"172.31.255.255", true}, // end of class-B private range
		{"172.32.0.1", false},    // just above the class-B private range
		{"192.168.1.1", true},
		{"169.254.0.1", true},
		{"8.8.8.8", false},
		{"100.64.0.1", false},  // CGNAT — not local
		{"fe80::1", true},      // IPv6 link-local
		{"fc00::1", true},      // IPv6 unique-local
		{"fd12::1", true},      // IPv6 unique-local
		{"fec0::1", true},      // IPv6 site-local (deprecated but accepted)
		{"2001:db8::1", false}, // IPv6 documentation prefix
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("parse %q", tc.ip)
			}
			if got := isLocalAddress(ip); got != tc.want {
				t.Errorf("isLocalAddress(%s): got %v want %v", tc.ip, got, tc.want)
			}
		})
	}
	if isLocalAddress(nil) {
		t.Errorf("isLocalAddress(nil) should be false")
	}
}

func TestIsCgnatAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ip   string
		want bool
	}{
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"100.63.255.255", false}, // just below the CGNAT range
		{"100.128.0.0", false},    // just above the CGNAT range
		{"10.0.0.1", false},
		{"::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("parse %q", tc.ip)
			}
			if got := isCgnatAddress(ip); got != tc.want {
				t.Errorf("isCgnatAddress(%s): got %v want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestRemoteIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"127.0.0.1:12345", "127.0.0.1"},
		{"[::1]:12345", "::1"},
		{"192.168.1.1:54321", "192.168.1.1"},
		{"127.0.0.1", "127.0.0.1"}, // missing port — fall-through
		{"garbage", ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tc.raw}
			ip := remoteIP(r)
			got := ""
			if ip != nil {
				got = ip.String()
			}
			if got != tc.want {
				t.Errorf("remoteIP(%q): got %q want %q", tc.raw, got, tc.want)
			}
		})
	}
}
