package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	t.Parallel()
	secret := SessionSecret("test-api-key")
	issued := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	cookie := SignSessionCookie("user-id-uuid", true, issued, secret)
	got, err := VerifySessionCookie(cookie, secret, issued.Add(time.Minute))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Identifier != "user-id-uuid" {
		t.Errorf("Identifier = %q", got.Identifier)
	}
	if !got.Persistent {
		t.Errorf("Persistent should be true")
	}
	if !got.IssuedAt.Equal(issued) {
		t.Errorf("IssuedAt = %v want %v", got.IssuedAt, issued)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	t.Parallel()
	secret := SessionSecret("key")
	cookie := SignSessionCookie("id", false, time.Now().UTC(), secret)
	parts := strings.SplitN(cookie, ".", 2)
	// Swap the signature for a different (but well-formed base64url)
	// value so signature comparison must fail.
	tampered := parts[0] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := VerifySessionCookie(tampered, secret, time.Now().UTC()); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("tampered cookie: got err=%v, want ErrInvalidSession", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	t.Parallel()
	cookie := SignSessionCookie("id", false, time.Now().UTC(), SessionSecret("key1"))
	if _, err := VerifySessionCookie(cookie, SessionSecret("key2"), time.Now().UTC()); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("wrong secret: got err=%v, want ErrInvalidSession", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	t.Parallel()
	secret := SessionSecret("key")
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cookie := SignSessionCookie("id", false, old, secret)
	// 8 days later → exceeds 7-day max age.
	now := old.Add(8 * 24 * time.Hour)
	if _, err := VerifySessionCookie(cookie, secret, now); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("expired cookie: got err=%v, want ErrInvalidSession", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	t.Parallel()
	secret := SessionSecret("key")
	cases := []string{"", "nodot", ".", "x.y", "@@@.@@@"}
	for _, tc := range cases {
		if _, err := VerifySessionCookie(tc, secret, time.Now().UTC()); !errors.Is(err, ErrInvalidSession) {
			t.Errorf("malformed %q: got err=%v", tc, err)
		}
	}
}

func TestDefaultSessionAttributes(t *testing.T) {
	t.Parallel()
	attrs := DefaultSessionAttributes("MyAuth", "/sonarr", true, true)
	if attrs.Path != "/sonarr/" {
		t.Errorf("Path = %q", attrs.Path)
	}
	if attrs.MaxAge != int(SessionMaxAge.Seconds()) {
		t.Errorf("MaxAge = %d", attrs.MaxAge)
	}
	if !attrs.Secure {
		t.Errorf("Secure should be true")
	}
	rendered := attrs.String("cookieval")
	for _, want := range []string{
		"MyAuth=cookieval",
		"Path=/sonarr/",
		"Max-Age=" + intToStr(int(SessionMaxAge.Seconds())),
		"SameSite=Lax",
		"HttpOnly",
		"Secure",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered cookie missing %q: %q", want, rendered)
		}
	}
}

func TestDefaultSessionAttributes_SessionOnly(t *testing.T) {
	t.Parallel()
	attrs := DefaultSessionAttributes("X", "", false, false)
	if attrs.MaxAge != 0 {
		t.Errorf("MaxAge should be 0 for session-only; got %d", attrs.MaxAge)
	}
	rendered := attrs.String("v")
	if strings.Contains(rendered, "Max-Age") {
		t.Errorf("session-only cookie should not have Max-Age: %q", rendered)
	}
}

func TestDefaultSessionAttributes_EmptyName(t *testing.T) {
	t.Parallel()
	attrs := DefaultSessionAttributes("", "", false, false)
	if attrs.Name != "SonarrAuth" {
		t.Errorf("Name should default to SonarrAuth; got %q", attrs.Name)
	}
}

func intToStr(n int) string {
	// Avoid importing strconv just for one call in tests.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
