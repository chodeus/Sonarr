package configfile

import (
	"context"
	"errors"
	"os"
	"strconv"
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

// AnalyticsEnabled mirrors `ConfigFileProvider.cs:246`. Default true
// (C# `GetValueBoolean("AnalyticsEnabled", true, persist: false)`).
// The recording host had it disabled; production Go default stays
// true to match C# behaviour.
func (p *Provider) AnalyticsEnabled(ctx context.Context) (bool, error) {
	return p.Bool(ctx, "AnalyticsEnabled", true, false)
}

// ConsoleLogLevel mirrors `ConfigFileProvider.cs:251`. Default "".
func (p *Provider) ConsoleLogLevel(ctx context.Context) (string, error) {
	return p.String(ctx, "ConsoleLogLevel", "", false)
}

// LogSizeLimit mirrors `ConfigFileProvider.cs:271` (clamped 0..10 in
// C# — the Go port preserves the clamp). Default 1 MB.
func (p *Provider) LogSizeLimit(ctx context.Context) (int, error) {
	v, err := p.Int(ctx, "LogSizeLimit", 1, false)
	if err != nil {
		return 0, err
	}
	if v < 0 {
		v = 0
	}
	if v > 10 {
		v = 10
	}
	return v, nil
}

// AuthenticationRequired mirrors `ConfigFileProvider.cs:239-242`. C#
// returns an enum; the Go port emits the persisted PascalCase
// identifier ("Enabled" / "DisabledForLocalAddresses") for callers
// to camelCase at the API edge. Default "Enabled".
func (p *Provider) AuthenticationRequired(ctx context.Context) (string, error) {
	if p.over.AuthRequired != nil {
		return *p.over.AuthRequired, nil
	}
	return p.String(ctx, "AuthenticationRequired", "Enabled", true)
}

// TrustCgnatIpAddresses mirrors `ConfigFileProvider.cs:244`. Default
// false.
func (p *Provider) TrustCgnatIpAddresses(ctx context.Context) (bool, error) {
	return p.Bool(ctx, "TrustCgnatIpAddresses", false, false)
}

// SslCertPath mirrors `ConfigFileProvider.cs:273`. Default "".
// The Overrides struct doesn't carry an SslCertPath/Key/Password
// override slot yet — a follow-up adds them when the AppOptions env-
// var path lands.
func (p *Provider) SslCertPath(ctx context.Context) (string, error) {
	return p.String(ctx, "SslCertPath", "", true)
}

// SslKeyPath mirrors `ConfigFileProvider.cs:274`. Default "".
func (p *Provider) SslKeyPath(ctx context.Context) (string, error) {
	return p.String(ctx, "SslKeyPath", "", true)
}

// SslCertPassword mirrors `ConfigFileProvider.cs:275`. Default "".
func (p *Provider) SslCertPassword(ctx context.Context) (string, error) {
	return p.String(ctx, "SslCertPassword", "", true)
}

// UpdateAutomatically mirrors `ConfigFileProvider.cs:309`. C# uses
// `OsInfo.IsWindows` as the default — the Go port pins false (matches
// the non-Windows recording host); a follow-up can introduce host-
// platform-aware defaulting when needed.
func (p *Provider) UpdateAutomatically(ctx context.Context) (bool, error) {
	return p.Bool(ctx, "UpdateAutomatically", false, false)
}

// UpdateMechanism mirrors `ConfigFileProvider.cs:311-314`. Returns
// the persisted PascalCase identifier ("BuiltIn" / "Script" /
// "External" / "Apt" / "Docker"). Default "BuiltIn".
func (p *Provider) UpdateMechanism(ctx context.Context) (string, error) {
	return p.String(ctx, "UpdateMechanism", "BuiltIn", false)
}

// UpdateScriptPath mirrors `ConfigFileProvider.cs:316`. Default "".
func (p *Provider) UpdateScriptPath(ctx context.Context) (string, error) {
	return p.String(ctx, "UpdateScriptPath", "", false)
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

// SetTheme persists the UI theme to the XML config file. Used by the
// /api/v3/config/ui PUT handler (slice 5.B13b) — the C# tree saves
// Theme via ConfigFileProvider.SaveConfigDictionary; the Go port
// uses Set() directly because the wrapper around the XML doc is
// already key/value-shaped. An empty value is allowed (delete-key
// semantic is not exposed; an empty string is what the UI sends to
// reset to default).
func (p *Provider) SetTheme(ctx context.Context, value string) error {
	return p.Set(ctx, "Theme", value)
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

// ---------- Host PUT setters (slice 5.B13d) ----------
//
// These wrap *Provider.Set with the strconv conversion the C#
// SaveConfigDictionary fold performs implicitly. Bool values are
// serialised as "true"/"false" (matching the C# bool.ToString lowering
// at the XML write site); ints as base-10 digits.

func setBoolKey(ctx context.Context, p *Provider, key string, v bool) error {
	if v {
		return p.Set(ctx, key, "True")
	}
	return p.Set(ctx, key, "False")
}

func setIntKey(ctx context.Context, p *Provider, key string, v int) error {
	return p.Set(ctx, key, strconv.Itoa(v))
}

// SetBindAddress persists BindAddress.
func (p *Provider) SetBindAddress(ctx context.Context, v string) error {
	return p.Set(ctx, "BindAddress", v)
}

// SetPort persists Port.
func (p *Provider) SetPort(ctx context.Context, v int) error {
	return setIntKey(ctx, p, "Port", v)
}

// SetSslPort persists SslPort.
func (p *Provider) SetSslPort(ctx context.Context, v int) error {
	return setIntKey(ctx, p, "SslPort", v)
}

// SetEnableSsl persists EnableSsl.
func (p *Provider) SetEnableSsl(ctx context.Context, v bool) error {
	return setBoolKey(ctx, p, "EnableSsl", v)
}

// SetLaunchBrowser persists LaunchBrowser.
func (p *Provider) SetLaunchBrowser(ctx context.Context, v bool) error {
	return setBoolKey(ctx, p, "LaunchBrowser", v)
}

// SetAuthenticationMethod persists AuthenticationMethod (PascalCase
// identifier — "Forms", "None", "External"; "Basic" is rejected by
// the controller validator before reaching this setter).
func (p *Provider) SetAuthenticationMethod(ctx context.Context, v string) error {
	return p.Set(ctx, "AuthenticationMethod", v)
}

// SetAuthenticationRequired persists AuthenticationRequired (PascalCase
// identifier — "Enabled", "DisabledForLocalAddresses").
func (p *Provider) SetAuthenticationRequired(ctx context.Context, v string) error {
	return p.Set(ctx, "AuthenticationRequired", v)
}

// SetAnalyticsEnabled persists AnalyticsEnabled.
func (p *Provider) SetAnalyticsEnabled(ctx context.Context, v bool) error {
	return setBoolKey(ctx, p, "AnalyticsEnabled", v)
}

// SetLogLevel persists LogLevel (already-lowered).
func (p *Provider) SetLogLevel(ctx context.Context, v string) error {
	return p.Set(ctx, "LogLevel", v)
}

// SetLogSizeLimit persists LogSizeLimit (caller clamps to 1..10).
func (p *Provider) SetLogSizeLimit(ctx context.Context, v int) error {
	return setIntKey(ctx, p, "LogSizeLimit", v)
}

// SetConsoleLogLevel persists ConsoleLogLevel.
func (p *Provider) SetConsoleLogLevel(ctx context.Context, v string) error {
	return p.Set(ctx, "ConsoleLogLevel", v)
}

// SetBranch persists Branch (already-lowered).
func (p *Provider) SetBranch(ctx context.Context, v string) error {
	return p.Set(ctx, "Branch", v)
}

// SetSslCertPath persists SslCertPath.
func (p *Provider) SetSslCertPath(ctx context.Context, v string) error {
	return p.Set(ctx, "SslCertPath", v)
}

// SetSslKeyPath persists SslKeyPath.
func (p *Provider) SetSslKeyPath(ctx context.Context, v string) error {
	return p.Set(ctx, "SslKeyPath", v)
}

// SetSslCertPassword persists SslCertPassword.
func (p *Provider) SetSslCertPassword(ctx context.Context, v string) error {
	return p.Set(ctx, "SslCertPassword", v)
}

// SetURLBase persists UrlBase (caller normalises the slash shape).
func (p *Provider) SetURLBase(ctx context.Context, v string) error {
	return p.Set(ctx, "UrlBase", v)
}

// SetInstanceName persists InstanceName.
func (p *Provider) SetInstanceName(ctx context.Context, v string) error {
	return p.Set(ctx, "InstanceName", v)
}

// SetTrustCgnatIpAddresses persists TrustCgnatIpAddresses.
func (p *Provider) SetTrustCgnatIpAddresses(ctx context.Context, v bool) error {
	return setBoolKey(ctx, p, "TrustCgnatIpAddresses", v)
}

// SetUpdateAutomatically persists UpdateAutomatically.
func (p *Provider) SetUpdateAutomatically(ctx context.Context, v bool) error {
	return setBoolKey(ctx, p, "UpdateAutomatically", v)
}

// SetUpdateMechanism persists UpdateMechanism (PascalCase identifier).
func (p *Provider) SetUpdateMechanism(ctx context.Context, v string) error {
	return p.Set(ctx, "UpdateMechanism", v)
}

// SetUpdateScriptPath persists UpdateScriptPath.
func (p *Provider) SetUpdateScriptPath(ctx context.Context, v string) error {
	return p.Set(ctx, "UpdateScriptPath", v)
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
