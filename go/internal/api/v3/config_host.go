package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
)

// FileChecker reports whether path is an existing regular file. It
// mirrors the C# `FileExistsValidator(diskProvider)` applied to
// SslCertPath / SslKeyPath (`HostConfigController.cs:63,70`). Injected
// so the validator stays hermetic in tests; the production default
// ([osFileExists]) uses os.Stat.
type FileChecker func(path string) bool

// osFileExists is the production [FileChecker]: the path must exist and
// be a regular file (a directory doesn't satisfy DiskProvider.FileExists).
func osFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// HostConfigResource mirrors
// `src/Sonarr.Api.V3/Config/HostConfigResource.cs`. 39 fields covering
// every host-level knob (bind/port, ssl, branch, auth, proxy, backup,
// update, log). Username/Password/PasswordConfirmation are emitted as
// "" — the C# mapper intentionally never populates them (line 65-66
// "// Username / // Password"); the wire shape carries the
// empty-string defaults that the struct's zero values produce.
//
// Five fields camelCase a PascalCase-stored enum at the API edge:
// authenticationMethod, authenticationRequired, proxyType,
// certificateValidation, updateMechanism.
type HostConfigResource struct {
	ID                        int    `json:"id"`
	BindAddress               string `json:"bindAddress"`
	Port                      int    `json:"port"`
	SslPort                   int    `json:"sslPort"`
	EnableSsl                 bool   `json:"enableSsl"`
	LaunchBrowser             bool   `json:"launchBrowser"`
	AuthenticationMethod      string `json:"authenticationMethod"`
	AuthenticationRequired    string `json:"authenticationRequired"`
	AnalyticsEnabled          bool   `json:"analyticsEnabled"`
	Username                  string `json:"username"`
	Password                  string `json:"password"`
	PasswordConfirmation      string `json:"passwordConfirmation"`
	LogLevel                  string `json:"logLevel"`
	LogSizeLimit              int    `json:"logSizeLimit"`
	ConsoleLogLevel           string `json:"consoleLogLevel"`
	Branch                    string `json:"branch"`
	APIKey                    string `json:"apiKey"`
	SslCertPath               string `json:"sslCertPath"`
	SslKeyPath                string `json:"sslKeyPath"`
	SslCertPassword           string `json:"sslCertPassword"`
	URLBase                   string `json:"urlBase"`
	InstanceName              string `json:"instanceName"`
	ApplicationURL            string `json:"applicationUrl"`
	UpdateAutomatically       bool   `json:"updateAutomatically"`
	UpdateMechanism           string `json:"updateMechanism"`
	UpdateScriptPath          string `json:"updateScriptPath"`
	ProxyEnabled              bool   `json:"proxyEnabled"`
	ProxyType                 string `json:"proxyType"`
	ProxyHostname             string `json:"proxyHostname"`
	ProxyPort                 int    `json:"proxyPort"`
	ProxyUsername             string `json:"proxyUsername"`
	ProxyPassword             string `json:"proxyPassword"`
	ProxyBypassFilter         string `json:"proxyBypassFilter"`
	ProxyBypassLocalAddresses bool   `json:"proxyBypassLocalAddresses"`
	CertificateValidation     string `json:"certificateValidation"`
	BackupFolder              string `json:"backupFolder"`
	BackupInterval            int    `json:"backupInterval"`
	BackupRetention           int    `json:"backupRetention"`
	TrustCgnatIPAddresses     bool   `json:"trustCgnatIpAddresses"`
}

// HostConfigGetter is the composite read surface the handler needs.
// In production it's satisfied by a small adapter in
// cmd/sonarr/host_wiring.go combining *configfile.Provider (env+xml
// fields) and *config.Provider (DB-table fields: proxy, backup,
// applicationUrl, certificateValidation).
type HostConfigGetter interface {
	// configfile.Provider-backed (env+xml)
	BindAddress(ctx context.Context) (string, error)
	Port(ctx context.Context) (int, error)
	SslPort(ctx context.Context) (int, error)
	EnableSsl(ctx context.Context) (bool, error)
	LaunchBrowser(ctx context.Context) (bool, error)
	AuthenticationMethod(ctx context.Context) (string, error)
	AuthenticationRequired(ctx context.Context) (string, error)
	AnalyticsEnabled(ctx context.Context) (bool, error)
	LogLevel(ctx context.Context) (string, error)
	LogSizeLimit(ctx context.Context) (int, error)
	ConsoleLogLevel(ctx context.Context) (string, error)
	Branch(ctx context.Context) (string, error)
	APIKey(ctx context.Context) (string, error)
	SslCertPath(ctx context.Context) (string, error)
	SslKeyPath(ctx context.Context) (string, error)
	SslCertPassword(ctx context.Context) (string, error)
	URLBase(ctx context.Context) (string, error)
	InstanceName(ctx context.Context) (string, error)
	TrustCgnatIpAddresses(ctx context.Context) (bool, error)
	UpdateAutomatically(ctx context.Context) (bool, error)
	UpdateMechanism(ctx context.Context) (string, error)
	UpdateScriptPath(ctx context.Context) (string, error)

	// config.Provider-backed (DB)
	ApplicationUrl() string
	BackupFolder() string
	BackupInterval() int
	BackupRetention() int
	CertificateValidation() string
	ProxyEnabled() bool
	ProxyType() string
	ProxyHostname() string
	ProxyPort() int
	ProxyUsername() string
	ProxyPassword() string
	ProxyBypassFilter() string
	ProxyBypassLocalAddresses() bool
}

// HostConfigHandler returns the http.Handler for GET /api/v3/config/host.
// Mirrors `HostConfigController` — singleton with Id=1. Username,
// Password, and PasswordConfirmation are emitted as the empty-string
// defaults (C# mapper line 65-66 intentionally doesn't populate
// them).
func HostConfigHandler(getter HostConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		read := func(s string, err error) (string, bool) {
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/host: %w", err), nil)
				return "", false
			}
			return s, true
		}
		readInt := func(v int, err error) (int, bool) {
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/host: %w", err), nil)
				return 0, false
			}
			return v, true
		}
		readBool := func(v bool, err error) (bool, bool) {
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/host: %w", err), nil)
				return false, false
			}
			return v, true
		}

		bind, ok := read(getter.BindAddress(ctx))
		if !ok {
			return
		}
		port, ok := readInt(getter.Port(ctx))
		if !ok {
			return
		}
		sslPort, ok := readInt(getter.SslPort(ctx))
		if !ok {
			return
		}
		enableSsl, ok := readBool(getter.EnableSsl(ctx))
		if !ok {
			return
		}
		launchBrowser, ok := readBool(getter.LaunchBrowser(ctx))
		if !ok {
			return
		}
		authMethod, ok := read(getter.AuthenticationMethod(ctx))
		if !ok {
			return
		}
		authReq, ok := read(getter.AuthenticationRequired(ctx))
		if !ok {
			return
		}
		analytics, ok := readBool(getter.AnalyticsEnabled(ctx))
		if !ok {
			return
		}
		logLevel, ok := read(getter.LogLevel(ctx))
		if !ok {
			return
		}
		logSizeLimit, ok := readInt(getter.LogSizeLimit(ctx))
		if !ok {
			return
		}
		consoleLog, ok := read(getter.ConsoleLogLevel(ctx))
		if !ok {
			return
		}
		branch, ok := read(getter.Branch(ctx))
		if !ok {
			return
		}
		apiKey, ok := read(getter.APIKey(ctx))
		if !ok {
			return
		}
		sslCertPath, ok := read(getter.SslCertPath(ctx))
		if !ok {
			return
		}
		sslKeyPath, ok := read(getter.SslKeyPath(ctx))
		if !ok {
			return
		}
		sslCertPass, ok := read(getter.SslCertPassword(ctx))
		if !ok {
			return
		}
		urlBase, ok := read(getter.URLBase(ctx))
		if !ok {
			return
		}
		instance, ok := read(getter.InstanceName(ctx))
		if !ok {
			return
		}
		trustCgnat, ok := readBool(getter.TrustCgnatIpAddresses(ctx))
		if !ok {
			return
		}
		updateAuto, ok := readBool(getter.UpdateAutomatically(ctx))
		if !ok {
			return
		}
		updateMech, ok := read(getter.UpdateMechanism(ctx))
		if !ok {
			return
		}
		updateScript, ok := read(getter.UpdateScriptPath(ctx))
		if !ok {
			return
		}

		res := HostConfigResource{
			ID:                        1,
			BindAddress:               bind,
			Port:                      port,
			SslPort:                   sslPort,
			EnableSsl:                 enableSsl,
			LaunchBrowser:             launchBrowser,
			AuthenticationMethod:      CamelCaseEnumName(authMethod),
			AuthenticationRequired:    CamelCaseEnumName(authReq),
			AnalyticsEnabled:          analytics,
			LogLevel:                  logLevel,
			LogSizeLimit:              logSizeLimit,
			ConsoleLogLevel:           consoleLog,
			Branch:                    branch,
			APIKey:                    apiKey,
			SslCertPath:               sslCertPath,
			SslKeyPath:                sslKeyPath,
			SslCertPassword:           sslCertPass,
			URLBase:                   urlBase,
			InstanceName:              instance,
			ApplicationURL:            getter.ApplicationUrl(),
			UpdateAutomatically:       updateAuto,
			UpdateMechanism:           CamelCaseEnumName(updateMech),
			UpdateScriptPath:          updateScript,
			ProxyEnabled:              getter.ProxyEnabled(),
			ProxyType:                 CamelCaseEnumName(getter.ProxyType()),
			ProxyHostname:             getter.ProxyHostname(),
			ProxyPort:                 getter.ProxyPort(),
			ProxyUsername:             getter.ProxyUsername(),
			ProxyPassword:             getter.ProxyPassword(),
			ProxyBypassFilter:         getter.ProxyBypassFilter(),
			ProxyBypassLocalAddresses: getter.ProxyBypassLocalAddresses(),
			CertificateValidation:     CamelCaseEnumName(getter.CertificateValidation()),
			BackupFolder:              getter.BackupFolder(),
			BackupInterval:            getter.BackupInterval(),
			BackupRetention:           getter.BackupRetention(),
			TrustCgnatIPAddresses:     trustCgnat,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// HostConfigWriter is the write surface: getter + per-field setters
// across both backends (configfile-backed env+xml fields and
// config.Provider-backed DB fields).
//
// In production a small adapter combines *configfile.Provider and
// *config.Provider — the same pattern the read-side HostConfigGetter
// uses. The 5.B13d setter surface deliberately omits APIKey (rotated
// via ResetApiKeyCommand only), Username/Password (UserService.Upsert
// — DEFERRED), and TestId/PasswordConfirmation (not persisted).
type HostConfigWriter interface {
	HostConfigGetter

	// configfile-backed setters
	SetBindAddress(ctx context.Context, v string) error
	SetPort(ctx context.Context, v int) error
	SetSslPort(ctx context.Context, v int) error
	SetEnableSsl(ctx context.Context, v bool) error
	SetLaunchBrowser(ctx context.Context, v bool) error
	SetAuthenticationMethod(ctx context.Context, v string) error
	SetAuthenticationRequired(ctx context.Context, v string) error
	SetAnalyticsEnabled(ctx context.Context, v bool) error
	SetLogLevel(ctx context.Context, v string) error
	SetLogSizeLimit(ctx context.Context, v int) error
	SetConsoleLogLevel(ctx context.Context, v string) error
	SetBranch(ctx context.Context, v string) error
	SetSslCertPath(ctx context.Context, v string) error
	SetSslKeyPath(ctx context.Context, v string) error
	SetSslCertPassword(ctx context.Context, v string) error
	SetURLBase(ctx context.Context, v string) error
	SetInstanceName(ctx context.Context, v string) error
	SetTrustCgnatIpAddresses(ctx context.Context, v bool) error
	SetUpdateAutomatically(ctx context.Context, v bool) error
	SetUpdateMechanism(ctx context.Context, v string) error
	SetUpdateScriptPath(ctx context.Context, v string) error

	// config.Provider-backed setters
	SetApplicationUrl(ctx context.Context, v string) error
	SetBackupFolder(ctx context.Context, v string) error
	SetBackupInterval(ctx context.Context, v int) error
	SetBackupRetention(ctx context.Context, v int) error
	SetCertificateValidation(ctx context.Context, v string) error
	SetProxyEnabled(ctx context.Context, v bool) error
	SetProxyType(ctx context.Context, v string) error
	SetProxyHostname(ctx context.Context, v string) error
	SetProxyPort(ctx context.Context, v int) error
	SetProxyUsername(ctx context.Context, v string) error
	SetProxyPassword(ctx context.Context, v string) error
	SetProxyBypassFilter(ctx context.Context, v string) error
	SetProxyBypassLocalAddresses(ctx context.Context, v bool) error
}

// urlBasePattern mirrors the C# ValidUrlBase regex check. The C#
// ConfigFileValidation extension trims slashes then checks the result
// is empty OR an alphanumeric path. Leading/trailing slashes are
// normalised by URLBase getter, so a stored "//foo//" round-trips
// fine; the validator here rejects an inbound value with structurally
// invalid characters before persistence.
var urlBasePattern = regexp.MustCompile(`^[A-Za-z0-9_\-./]*$`)

// HostConfigUpdateHandler returns PUT /api/v3/config/host/{id}.
// Mirrors `HostConfigController.SaveHostConfig` — singleton with
// Id=1. The C# action calls `_configFileProvider.SaveConfigDictionary`
// AND `_configService.SaveConfigDictionary` with the same property
// dictionary; each provider filters to its own key set. The Go port
// replicates the split by routing each field through the typed setter
// on whichever backend owns the key.
//
// The C# action also calls `_userService.Upsert(username, password)`
// when both are non-empty; the Go port DEFERS this — Username/Password
// validation is included (NotEmpty when AuthenticationMethod==Forms +
// PasswordConfirmation must match Password) but the actual
// UserService write is a follow-up slice once the users package
// exposes an Upsert-with-hash entry point. The setter slice silently
// skips Username/Password fields.
//
// Validation chain (cheap subset of C# SharedValidator at controller
// :35-80):
//   - BindAddress: when not "*"/"localhost", must parse as net.IP
//   - Port: 1..65535
//   - SslPort: 1..65535 AND != Port, both gated on EnableSsl
//   - UrlBase: regex check for alphanumeric+./-_
//   - InstanceName: starts-or-ends-with "Sonarr"
//   - AuthenticationMethod: not "Basic" (deprecated)
//   - Username/Password: NotEmpty when AuthenticationMethod==Forms
//   - PasswordConfirmation: equals Password
//   - SslCertPath: NotEmpty + absolute + FileExists when EnableSsl
//   - SslKeyPath: absolute + FileExists when set (any non-empty value)
//   - LogSizeLimit: 1..10
//   - Branch: NotEmpty
//   - UpdateScriptPath: absolute when UpdateMechanism==Script
//   - BackupFolder: absolute when non-empty
//   - BackupInterval: 1..7
//   - BackupRetention: 1..90
//
// J404 closes the SslCertPath/SslKeyPath FileExists arm of the deferral
// (C# `:63,70` `FileExistsValidator`). The optional fileExists arg is a
// [FileChecker]; when omitted the production [osFileExists] (os.Stat) is
// used, so the file-existence check is never silently skipped. DEFERRED
// still: SslCertPath IsValidCertificate (needs an X.509 parse adapter);
// UserService.Upsert (needs a hash-aware writer). PasswordConfirmation's
// user-svc short-circuit ("if the stored user's password matches, ignore
// the confirmation field") is also DEFERRED — the simpler equal-pair
// check rejects mismatches.
func HostConfigUpdateHandler(svc HostConfigWriter, fileExists ...FileChecker) http.Handler {
	check := osFileExists
	if len(fileExists) > 0 && fileExists[0] != nil {
		check = fileExists[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body HostConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/host: malformed body: %v", err))
			return
		}
		if errs := validateHostConfig(&body, check); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		// Decode the five wire-camelCase enums into the persisted
		// PascalCase identifier the setters expect. Validation already
		// guaranteed "Basic" isn't reaching SetAuthenticationMethod.
		authMethod, err := AuthenticationMethodFromName(body.AuthenticationMethod)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "AuthenticationMethod",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}
		authReq, err := AuthenticationRequiredFromName(body.AuthenticationRequired)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "AuthenticationRequired",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}
		updateMech, err := UpdateMechanismFromName(body.UpdateMechanism)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "UpdateMechanism",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}
		var proxyType string
		if body.ProxyEnabled {
			proxyType, err = ProxyTypeFromName(body.ProxyType)
			if err != nil {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "ProxyType",
					ErrorMessage: err.Error(),
					Severity:     "error",
				}})
				return
			}
		}
		certValidation, err := CertificateValidationFromName(body.CertificateValidation)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "CertificateValidation",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}

		ctx := r.Context()
		type setter struct {
			name string
			fn   func() error
		}
		setters := []setter{
			{"BindAddress", func() error { return svc.SetBindAddress(ctx, body.BindAddress) }},
			{"Port", func() error { return svc.SetPort(ctx, body.Port) }},
			{"SslPort", func() error { return svc.SetSslPort(ctx, body.SslPort) }},
			{"EnableSsl", func() error { return svc.SetEnableSsl(ctx, body.EnableSsl) }},
			{"LaunchBrowser", func() error { return svc.SetLaunchBrowser(ctx, body.LaunchBrowser) }},
			{"AuthenticationMethod", func() error { return svc.SetAuthenticationMethod(ctx, authMethod) }},
			{"AuthenticationRequired", func() error { return svc.SetAuthenticationRequired(ctx, authReq) }},
			{"AnalyticsEnabled", func() error { return svc.SetAnalyticsEnabled(ctx, body.AnalyticsEnabled) }},
			{"LogLevel", func() error { return svc.SetLogLevel(ctx, strings.ToLower(strings.TrimSpace(body.LogLevel))) }},
			{"LogSizeLimit", func() error { return svc.SetLogSizeLimit(ctx, body.LogSizeLimit) }},
			{"ConsoleLogLevel", func() error { return svc.SetConsoleLogLevel(ctx, body.ConsoleLogLevel) }},
			{"Branch", func() error { return svc.SetBranch(ctx, strings.ToLower(strings.TrimSpace(body.Branch))) }},
			{"SslCertPath", func() error { return svc.SetSslCertPath(ctx, body.SslCertPath) }},
			{"SslKeyPath", func() error { return svc.SetSslKeyPath(ctx, body.SslKeyPath) }},
			{"SslCertPassword", func() error { return svc.SetSslCertPassword(ctx, body.SslCertPassword) }},
			{"UrlBase", func() error { return svc.SetURLBase(ctx, body.URLBase) }},
			{"InstanceName", func() error { return svc.SetInstanceName(ctx, body.InstanceName) }},
			{"TrustCgnatIpAddresses", func() error { return svc.SetTrustCgnatIpAddresses(ctx, body.TrustCgnatIPAddresses) }},
			{"UpdateAutomatically", func() error { return svc.SetUpdateAutomatically(ctx, body.UpdateAutomatically) }},
			{"UpdateMechanism", func() error { return svc.SetUpdateMechanism(ctx, updateMech) }},
			{"UpdateScriptPath", func() error { return svc.SetUpdateScriptPath(ctx, body.UpdateScriptPath) }},
			{"ApplicationUrl", func() error { return svc.SetApplicationUrl(ctx, body.ApplicationURL) }},
			{"BackupFolder", func() error { return svc.SetBackupFolder(ctx, body.BackupFolder) }},
			{"BackupInterval", func() error { return svc.SetBackupInterval(ctx, body.BackupInterval) }},
			{"BackupRetention", func() error { return svc.SetBackupRetention(ctx, body.BackupRetention) }},
			{"CertificateValidation", func() error { return svc.SetCertificateValidation(ctx, certValidation) }},
			{"ProxyEnabled", func() error { return svc.SetProxyEnabled(ctx, body.ProxyEnabled) }},
			{"ProxyType", func() error {
				if !body.ProxyEnabled {
					return nil
				}
				return svc.SetProxyType(ctx, proxyType)
			}},
			{"ProxyHostname", func() error { return svc.SetProxyHostname(ctx, body.ProxyHostname) }},
			{"ProxyPort", func() error { return svc.SetProxyPort(ctx, body.ProxyPort) }},
			{"ProxyUsername", func() error { return svc.SetProxyUsername(ctx, body.ProxyUsername) }},
			{"ProxyPassword", func() error { return svc.SetProxyPassword(ctx, body.ProxyPassword) }},
			{"ProxyBypassFilter", func() error { return svc.SetProxyBypassFilter(ctx, body.ProxyBypassFilter) }},
			{"ProxyBypassLocalAddresses", func() error { return svc.SetProxyBypassLocalAddresses(ctx, body.ProxyBypassLocalAddresses) }},
		}
		for _, s := range setters {
			if err := s.fn(); err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/host: set %s: %w", s.name, err), nil)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		// Echo the inbound resource (matches C# `Accepted(resource.Id)`
		// + downstream rest helper that round-trips the input body).
		// Rebuilding from the getter would be ideal but requires
		// re-reading 22 configfile entries — the per-field setter slice
		// is enough proof of persistence.
		buf, _ := json.Marshal(body)
		_, _ = w.Write(buf)
	})
}

// validateHostConfig ports the cheap subset of HostConfigController's
// SharedValidator chain. See HostConfigUpdateHandler doc for the
// deferred filesystem-state checks.
func validateHostConfig(res *HostConfigResource, fileExists FileChecker) *validation.Errors {
	errs := validation.New()

	// BindAddress: ValidIpAddress when not "*" / "localhost".
	if res.BindAddress != "*" && res.BindAddress != "localhost" && res.BindAddress != "" {
		if net.ParseIP(res.BindAddress) == nil {
			errs.Add("BindAddress", "BindAddress must be * or localhost or a valid IP address")
		}
	}

	// Port: ValidPort (1..65535).
	if res.Port < 1 || res.Port > 65535 {
		errs.Add("Port", "Port must be between 1 and 65535")
	}

	// SslPort gated on EnableSsl.
	if res.EnableSsl {
		if res.SslPort < 1 || res.SslPort > 65535 {
			errs.Add("SslPort", "SslPort must be between 1 and 65535")
		} else if res.SslPort == res.Port {
			errs.Add("SslPort", "SslPort must not equal Port")
		}
		if strings.TrimSpace(res.SslCertPath) == "" {
			errs.Add("SslCertPath", "SslCertPath is required when SSL is enabled")
		} else if !filepath.IsAbs(res.SslCertPath) {
			errs.Add("SslCertPath", "SslCertPath must be an absolute path")
		} else if fileExists != nil && !fileExists(res.SslCertPath) {
			// C# FileExistsValidator on SslCertPath, When(EnableSsl).
			errs.Add("SslCertPath", "SslCertPath file does not exist")
		}
	}

	// SslKeyPath is optional but when set must be absolute and exist on
	// disk (C# FileExistsValidator on SslKeyPath, When(not empty)).
	if strings.TrimSpace(res.SslKeyPath) != "" {
		if !filepath.IsAbs(res.SslKeyPath) {
			errs.Add("SslKeyPath", "SslKeyPath must be an absolute path")
		} else if fileExists != nil && !fileExists(res.SslKeyPath) {
			errs.Add("SslKeyPath", "SslKeyPath file does not exist")
		}
	}

	// UrlBase regex.
	if !urlBasePattern.MatchString(res.URLBase) {
		errs.Add("UrlBase", "UrlBase contains invalid characters")
	}

	// InstanceName must start or end with "Sonarr" (C# StartsOrEndsWithSonarr).
	if !strings.HasPrefix(res.InstanceName, "Sonarr") && !strings.HasSuffix(res.InstanceName, "Sonarr") {
		errs.Add("InstanceName", "InstanceName must start with or end with 'Sonarr'")
	}

	// AuthenticationMethod: deprecated "Basic" rejected at the wire
	// (camelCase "basic"). The handler emits the C# message verbatim.
	if res.AuthenticationMethod == "basic" {
		errs.Add("AuthenticationMethod", "'Basic' is no longer supported, switch to 'Forms' instead.")
	}

	// Forms auth requires username + password.
	if res.AuthenticationMethod == "forms" {
		if strings.TrimSpace(res.Username) == "" {
			errs.Add("Username", "Username is required when using Forms authentication")
		}
		if strings.TrimSpace(res.Password) == "" {
			errs.Add("Password", "Password is required when using Forms authentication")
		}
	}

	// PasswordConfirmation must match Password (simpler than the C#
	// user-svc short-circuit; see handler-doc DEFERRED note).
	if res.Password != res.PasswordConfirmation {
		errs.Add("PasswordConfirmation", "Must match Password")
	}

	// LogSizeLimit: 1..10.
	errs.Range("LogSizeLimit", res.LogSizeLimit, 1, 10)

	// Branch: NotEmpty.
	if strings.TrimSpace(res.Branch) == "" {
		errs.Add("Branch", "Branch name is required, 'main' is the default")
	}

	// UpdateScriptPath: absolute when UpdateMechanism==Script.
	if res.UpdateMechanism == "script" && strings.TrimSpace(res.UpdateScriptPath) != "" && !filepath.IsAbs(res.UpdateScriptPath) {
		errs.Add("UpdateScriptPath", "UpdateScriptPath must be an absolute path when UpdateMechanism is Script")
	}

	// BackupFolder: absolute when non-empty.
	if strings.TrimSpace(res.BackupFolder) != "" && !filepath.IsAbs(res.BackupFolder) {
		errs.Add("BackupFolder", "BackupFolder must be an absolute path when provided")
	}

	// BackupInterval: 1..7.
	errs.Range("BackupInterval", res.BackupInterval, 1, 7)

	// BackupRetention: 1..90.
	errs.Range("BackupRetention", res.BackupRetention, 1, 90)

	return errs
}
