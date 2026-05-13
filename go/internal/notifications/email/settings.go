// Package email ports the C# `NzbDrone.Core.Notifications.Email`
// concrete notification provider. Slice 4.CJ (C-cleanup [C3]
// sub-arc C3i).
//
// Surface: Settings + Validate + EncryptionType enum + Proxy (SMTP
// dial/auth/send via net/smtp + crypto/tls for the implicit-SSL or
// STARTTLS upgrade) + all 11 OnX hooks.
//
// Encryption strategy mirrors the C# `EmailEncryptionType` switch at
// `Email.cs:144-153`:
//   - Preferred (0): STARTTLS opportunistic on standard ports.
//   - Always (1): SslOnConnect when Port=465, STARTTLS otherwise.
//   - Never (2): plain SMTP.
package email

import (
	"net/mail"
	"strings"

	"sonarr.tv/sonarr/internal/notifications"
)

// EncryptionType mirrors the C# enum at `EmailSettings.cs:74-79`.
// Persisted as int on Settings.
type EncryptionType int

// EncryptionType values mirror C# enum-name → int.
const (
	EncryptionPreferred EncryptionType = 0
	EncryptionAlways    EncryptionType = 1
	EncryptionNever     EncryptionType = 2
)

// Settings ports `EmailSettings` at
// `Notifications/Email/EmailSettings.cs:28-72`.
type Settings struct {
	// Server is the SMTP host — REQUIRED. C# `:42`.
	Server string `json:"server"`
	// Port is the SMTP port — defaults to 587. C# `:45`.
	Port int `json:"port"`
	// UseEncryption is the int-encoded [EncryptionType]. C# `:48`.
	UseEncryption int `json:"useEncryption"`
	// Username is the optional SMTP auth username. C# `:51`.
	Username string `json:"username"`
	// Password is the optional SMTP auth password. C# `:54`.
	Password string `json:"password"`
	// From is the sender email address — REQUIRED. C# `:57`.
	From string `json:"from"`
	// To is the per-event To address list. C# `:60`.
	To []string `json:"to"`
	// Cc is the per-event CC address list. C# `:63`.
	Cc []string `json:"cc"`
	// Bcc is the per-event BCC address list. C# `:66`.
	Bcc []string `json:"bcc"`
}

// Validate ports the C# `EmailSettingsValidator` at `:10-25`.
//
// Rules:
//   - Server non-empty.
//   - Port in [1, 65535].
//   - From non-empty.
//   - Every To/Cc/Bcc entry parses as a valid email.
//   - At least one of To/Cc/Bcc non-empty.
func (s *Settings) Validate() []notifications.TestFailure {
	var out []notifications.TestFailure
	if strings.TrimSpace(s.Server) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "Server", ErrorMessage: "'Server' must not be empty."})
	}
	if s.Port < 1 || s.Port > 65535 {
		out = append(out, notifications.TestFailure{PropertyName: "Port", ErrorMessage: "'Port' must be between 1 and 65535."})
	}
	if strings.TrimSpace(s.From) == "" {
		out = append(out, notifications.TestFailure{PropertyName: "From", ErrorMessage: "'From' must not be empty."})
	}
	if len(s.To) == 0 && len(s.Cc) == 0 && len(s.Bcc) == 0 {
		out = append(out, notifications.TestFailure{PropertyName: "To", ErrorMessage: "Set at least one of To, Cc, Bcc."})
	}
	for _, a := range s.To {
		if !isEmail(a) {
			out = append(out, notifications.TestFailure{PropertyName: "To", ErrorMessage: "Invalid email address: " + a})
		}
	}
	for _, a := range s.Cc {
		if !isEmail(a) {
			out = append(out, notifications.TestFailure{PropertyName: "Cc", ErrorMessage: "Invalid email address: " + a})
		}
	}
	for _, a := range s.Bcc {
		if !isEmail(a) {
			out = append(out, notifications.TestFailure{PropertyName: "Bcc", ErrorMessage: "Invalid email address: " + a})
		}
	}
	return out
}

func isEmail(a string) bool {
	if strings.TrimSpace(a) == "" {
		return false
	}
	_, err := mail.ParseAddress(a)
	return err == nil
}
