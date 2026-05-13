package configfile

import (
	"context"
	"errors"
	"os"
	"strings"
)

// AppName is the canonical product name. Used by InstanceName's clamp
// rule (the C# code reads this from BuildInfo.AppName, which is
// frozen at "Sonarr"). Phase 1 hard-codes it; if/when the buildinfo
// package starts threading branding overrides through, this becomes
// a function.
const AppName = "Sonarr"

// BindAddress returns the configured bind address. Defaults to "*".
// Whitespace-only values fall back to the default (matches the C#
// `if string.IsNullOrWhiteSpace(bindAddress) return defaultValue`
// guard, which protects against an env var being set but empty).
func (p *Provider) BindAddress(ctx context.Context) (string, error) {
	const def = "*"
	v, err := p.String(ctx, "BindAddress", def, true)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return def, nil
	}
	return v, nil
}

// Port returns the HTTP port. Defaults to 8989.
func (p *Provider) Port(ctx context.Context) (int, error) {
	return p.Int(ctx, "Port", 8989, true)
}

// SslPort returns the HTTPS port. Defaults to 9898.
func (p *Provider) SslPort(ctx context.Context) (int, error) {
	return p.Int(ctx, "SslPort", 9898, true)
}

// EnableSsl reports whether HTTPS is on. Defaults to false.
func (p *Provider) EnableSsl(ctx context.Context) (bool, error) {
	return p.Bool(ctx, "EnableSsl", false, true)
}

// LaunchBrowser reports whether the host launches the system browser
// at startup. Defaults to true. (C# defaults this to true on every
// platform; the Os-conditional default lives only on UpdateAutomatically.)
func (p *Provider) LaunchBrowser(ctx context.Context) (bool, error) {
	return p.Bool(ctx, "LaunchBrowser", true, true)
}

// APIKey returns the configured API key, generating + persisting one
// if missing. Mirrors the C# ApiKey getter: empty / whitespace-only
// stored values trigger a fresh generation and SetValue write so the
// next read sees a stable value.
//
// Returns an error if the configured generator returns one (e.g.
// crypto/rand failure) or if the persistence write fails.
func (p *Provider) APIKey(ctx context.Context) (string, error) {
	if p.over.APIKey != nil {
		return *p.over.APIKey, nil
	}
	p.l.mu.Lock()
	v, ok := p.l.doc.get("ApiKey")
	p.l.mu.Unlock()
	if ok && strings.TrimSpace(v) != "" {
		return v, nil
	}
	if p.gen == nil {
		return "", errors.New("configfile: ApiKey missing and no generator configured")
	}
	fresh, err := p.gen()
	if err != nil {
		return "", err
	}
	if err := p.Set(ctx, "ApiKey", fresh); err != nil {
		return "", err
	}
	return fresh, nil
}

// ResetAPIKey forces a fresh API key, persisting it. Mirrors
// `ResetApiKeyCommand.Execute`. Returns the new key.
func (p *Provider) ResetAPIKey(ctx context.Context) (string, error) {
	if p.gen == nil {
		return "", errors.New("configfile: ResetAPIKey requires a generator")
	}
	fresh, err := p.gen()
	if err != nil {
		return "", err
	}
	if err := p.Set(ctx, "ApiKey", fresh); err != nil {
		return "", err
	}
	return fresh, nil
}

// URLBase returns the URL base prefix. Stored values are trimmed of
// surrounding slashes and prefixed with a single leading slash; an
// empty / all-slash value normalises to "" (no prefix).
func (p *Provider) URLBase(ctx context.Context) (string, error) {
	v, err := p.String(ctx, "UrlBase", "", true)
	if err != nil {
		return "", err
	}
	v = strings.Trim(v, "/")
	if v == "" {
		return "", nil
	}
	return "/" + v, nil
}

// InstanceName returns the instance display name. Defaults to
// AppName ("Sonarr"). C# parity rule: a configured value that
// neither starts nor ends with AppName falls back to AppName — this
// is to keep external tooling that string-matches "Sonarr" working
// even when the operator picks a custom name.
func (p *Provider) InstanceName(ctx context.Context) (string, error) {
	v, err := p.String(ctx, "InstanceName", AppName, true)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(v, AppName) || strings.HasSuffix(v, AppName) {
		return v, nil
	}
	return AppName, nil
}

// LogLevel returns the configured log level, lowercased. Default
// "debug".
func (p *Provider) LogLevel(ctx context.Context) (string, error) {
	v, err := p.String(ctx, "LogLevel", "debug", true)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(v)), nil
}

// Branch returns the configured update branch. Default "main",
// lowercased.
func (p *Provider) Branch(ctx context.Context) (string, error) {
	v, err := p.String(ctx, "Branch", "main", true)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(v)), nil
}

// Theme returns the configured UI theme, defaulting to "auto".
// Mirrors `ConfigFileProvider.Theme` at
// `src/NzbDrone.Core/Configuration/ConfigFileProvider.cs:258`. C#
// reads `_appOptions.Theme ?? GetValue("Theme", "auto", persist:
// false)` — the AppOptions env-var override slot belongs in the
// Overrides struct in a follow-up; this getter currently reads the
// XML value with the same persist=false discipline.
func (p *Provider) Theme(ctx context.Context) (string, error) {
	v, err := p.String(ctx, "Theme", "auto", false)
	if err != nil {
		return "", err
	}
	return v, nil
}

// AuthenticationMethod returns the configured auth scheme. Mirrors
// the C# AuthenticationMethod getter:
//   - if the legacy AuthenticationEnabled flag is true → Forms
//   - if the value parses to "Basic" (a deprecated shape) → Forms
//   - otherwise the parsed value, defaulting to None
//
// Returns the canonical name as a string ("Forms", "Basic", "None",
// "External"). The full enum lives in `internal/auth` once that
// package adds typed scheme dispatch in Phase 5.
func (p *Provider) AuthenticationMethod(ctx context.Context) (string, error) {
	if p.over.AuthMethod != nil {
		v := strings.TrimSpace(*p.over.AuthMethod)
		if strings.EqualFold(v, "Basic") {
			if err := p.Set(ctx, "AuthenticationMethod", "Forms"); err != nil {
				return "", err
			}
			return "Forms", nil
		}
		return v, nil
	}
	enabled, err := p.Bool(ctx, "AuthenticationEnabled", false, false)
	if err != nil {
		return "", err
	}
	if enabled {
		if err := p.Set(ctx, "AuthenticationMethod", "Forms"); err != nil {
			return "", err
		}
		return "Forms", nil
	}
	v, err := p.String(ctx, "AuthenticationMethod", "None", true)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(v, "Basic") {
		if err := p.Set(ctx, "AuthenticationMethod", "Forms"); err != nil {
			return "", err
		}
		return "Forms", nil
	}
	return v, nil
}

// osStatImpl bridges the package-level fake to the real os.Stat. Kept
// in this file so loader.go stays focused on parse / encode logic.
func osStatImpl(path string) (any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return info, nil
}
