package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/smtp"
	"strconv"
	"strings"
)

// ErrSendFailed wraps any transport-level failure (DNS / TCP / TLS /
// auth / SMTP RCPT). Mirrors C# `Exception` catch at `Email.cs:188-190`.
var ErrSendFailed = errors.New("email: send failed")

// smtpDialer is the narrow surface the proxy needs. Production uses
// [defaultDialer] which wraps net/smtp + crypto/tls. Tests inject a
// stub that records the SMTP command sequence.
type smtpDialer interface {
	Send(ctx context.Context, settings *Settings, from string, recipients []string, raw []byte) error
}

// dialer is the package-global smtpDialer — replaced by tests via
// [SetDialerForTest].
var dialer smtpDialer = defaultDialer{}

// SetDialerForTest swaps the package smtpDialer. Passing nil restores
// the production [defaultDialer]. Intended only for tests.
func SetDialerForTest(d smtpDialer) {
	if d == nil {
		dialer = defaultDialer{}
		return
	}
	dialer = d
}

// defaultDialer is the production smtpDialer implementation —
// dispatches on Settings.UseEncryption to the appropriate net/smtp
// connection flavor.
type defaultDialer struct{}

// Send dials the SMTP server, optionally upgrades to TLS, optionally
// authenticates, and sends the raw RFC 5322 message. Mirrors C#
// `Email.Send` at `:139-177`.
func (defaultDialer) Send(ctx context.Context, settings *Settings, from string, recipients []string, raw []byte) error {
	addr := settings.Server + ":" + strconv.Itoa(settings.Port)
	useEnc := EncryptionType(settings.UseEncryption)

	var c *smtp.Client
	var err error
	switch {
	case useEnc == EncryptionAlways && settings.Port == 465:
		// Implicit-TLS path mirrors C# SslOnConnect.
		conn, derr := tls.Dial("tcp", addr, &tls.Config{ServerName: settings.Server, MinVersion: tls.VersionTLS12})
		if derr != nil {
			return fmt.Errorf("%w: tls dial: %w", ErrSendFailed, derr)
		}
		c, err = smtp.NewClient(conn, settings.Server)
	default:
		c, err = smtp.Dial(addr)
	}
	if err != nil {
		return fmt.Errorf("%w: dial: %w", ErrSendFailed, err)
	}
	defer func() { _ = c.Close() }()

	// STARTTLS upgrade when configured to do so. The implicit-TLS
	// case (Always + 465) is already wrapped above; everything else
	// goes through opportunistic or mandatory STARTTLS.
	if useEnc != EncryptionNever && (useEnc != EncryptionAlways || settings.Port != 465) {
		if ok, _ := c.Extension("STARTTLS"); ok {
			tlsConfig := &tls.Config{ServerName: settings.Server, MinVersion: tls.VersionTLS12}
			if err := c.StartTLS(tlsConfig); err != nil {
				if useEnc == EncryptionAlways {
					return fmt.Errorf("%w: starttls: %w", ErrSendFailed, err)
				}
				// EncryptionPreferred: STARTTLS optional — proceed plain.
			}
		} else if useEnc == EncryptionAlways {
			return fmt.Errorf("%w: server does not advertise STARTTLS", ErrSendFailed)
		}
	}

	if strings.TrimSpace(settings.Username) != "" {
		auth := smtp.PlainAuth("", settings.Username, settings.Password, settings.Server)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("%w: auth: %w", ErrSendFailed, err)
		}
	}

	if err := c.Mail(from); err != nil {
		return fmt.Errorf("%w: mail-from: %w", ErrSendFailed, err)
	}
	for _, r := range recipients {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("%w: rcpt-to %s: %w", ErrSendFailed, r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("%w: data: %w", ErrSendFailed, err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("%w: write body: %w", ErrSendFailed, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("%w: close body: %w", ErrSendFailed, err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("%w: quit: %w", ErrSendFailed, err)
	}
	return nil
}

// sendEmail composes a minimal RFC 5322 message + fans out via the
// package dialer. Mirrors C# `SendEmail` at `Email.cs:107-137`.
func sendEmail(ctx context.Context, settings *Settings, subject, body string) error {
	var sb strings.Builder
	sb.WriteString("From: ")
	sb.WriteString(settings.From)
	sb.WriteString("\r\n")
	if len(settings.To) > 0 {
		sb.WriteString("To: ")
		sb.WriteString(strings.Join(settings.To, ", "))
		sb.WriteString("\r\n")
	}
	if len(settings.Cc) > 0 {
		sb.WriteString("Cc: ")
		sb.WriteString(strings.Join(settings.Cc, ", "))
		sb.WriteString("\r\n")
	}
	sb.WriteString("Subject: ")
	sb.WriteString(subject)
	sb.WriteString("\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)

	recipients := make([]string, 0, len(settings.To)+len(settings.Cc)+len(settings.Bcc))
	recipients = append(recipients, settings.To...)
	recipients = append(recipients, settings.Cc...)
	recipients = append(recipients, settings.Bcc...)
	if len(recipients) == 0 {
		return fmt.Errorf("%w: no recipients", ErrSendFailed)
	}

	return dialer.Send(ctx, settings, settings.From, recipients, []byte(sb.String()))
}
