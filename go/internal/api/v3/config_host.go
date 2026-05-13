package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

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
				http.Error(w, fmt.Sprintf("config/host: %v", err), http.StatusInternalServerError)
				return "", false
			}
			return s, true
		}
		readInt := func(v int, err error) (int, bool) {
			if err != nil {
				http.Error(w, fmt.Sprintf("config/host: %v", err), http.StatusInternalServerError)
				return 0, false
			}
			return v, true
		}
		readBool := func(v bool, err error) (bool, bool) {
			if err != nil {
				http.Error(w, fmt.Sprintf("config/host: %v", err), http.StatusInternalServerError)
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
