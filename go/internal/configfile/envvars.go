package configfile

import (
	"strconv"
	"strings"
)

// EnvPrefix is the ASP.NET-config-style prefix Sonarr's C# host
// honours via `AddEnvironmentVariables()` at
// `src/NzbDrone.Host/Bootstrap.cs:266`. Each `__` in the env-var
// name maps to a `:` in the config key path, e.g. `Sonarr__Server__Port`
// → config key `Sonarr:Server:Port` → `ServerOptions.Port`. Section
// names are case-insensitive in ASP.NET.
const EnvPrefix = "Sonarr"

// ParseOverridesFromEnv walks `os.Environ()`-formatted entries
// (`KEY=VALUE`) and returns an [Overrides] populated from any
// `Sonarr__Section__Key` style assignments. Mirrors C# Sonarr's env
// override surface — the four options classes
// (`AppOptions` / `AuthOptions` / `ServerOptions` / `UpdateOptions`)
// at `src/NzbDrone.Common/Options/` map onto the Overrides fields:
//
//   - `Sonarr__Server__Port`              → Overrides.Port
//   - `Sonarr__Server__BindAddress`       → Overrides.BindAddress
//   - `Sonarr__Server__UrlBase`           → Overrides.URLBase
//   - `Sonarr__Server__EnableSsl`         → Overrides.EnableSsl
//   - `Sonarr__Server__SslPort`           → Overrides.SslPort
//   - `Sonarr__Auth__ApiKey`              → Overrides.APIKey
//   - `Sonarr__Auth__Method`              → Overrides.AuthMethod
//   - `Sonarr__Auth__Required`            → Overrides.AuthRequired
//   - `Sonarr__App__InstanceName`         → Overrides.InstanceName
//   - `Sonarr__App__LaunchBrowser`        → Overrides.LaunchBrowser
//   - `Sonarr__Update__Branch`            → Overrides.BranchOverride
//   - `Sonarr__LogLevel`                  → Overrides.LogLevel
//
// Section and key names are matched case-insensitively (ASP.NET
// config posture). Values that fail type conversion (e.g. non-int
// Port) are silently dropped — same behaviour as C#'s
// `config.GetValue<int?>(...)` which returns null on a parse miss.
//
// `LogLevel` is special-cased because C# reads it from the unprefixed
// `LogLevel` key (`ConfigFileProvider.cs:264-265`) — accept either
// `Sonarr__LogLevel` (preferred) or bare `LogLevel`.
func ParseOverridesFromEnv(env []string) Overrides {
	var o Overrides
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		value := kv[eq+1:]
		applyEnvAssignment(&o, name, value)
	}
	return o
}

func applyEnvAssignment(o *Overrides, name, value string) {
	// Match `Sonarr__<...>` (case-insensitive on the prefix).
	rest, ok := stripPrefixCI(name, EnvPrefix+"__")
	if !ok {
		return
	}
	// Direct (non-sectioned) overrides land on the bare key after the prefix.
	sep := strings.Index(rest, "__")
	if sep < 0 {
		if strings.EqualFold(rest, "LogLevel") {
			s := value
			o.LogLevel = &s
		}
		return
	}
	// Sectioned form: `Sonarr__<Section>__<Key>`.
	section := rest[:sep]
	key := rest[sep+2:]
	switch strings.ToLower(section) {
	case "server":
		applyServerEnv(o, key, value)
	case "auth":
		applyAuthEnv(o, key, value)
	case "app":
		applyAppEnv(o, key, value)
	case "update":
		applyUpdateEnv(o, key, value)
	}
}

func applyServerEnv(o *Overrides, key, value string) {
	switch strings.ToLower(key) {
	case "port":
		if v, err := strconv.Atoi(value); err == nil {
			o.Port = &v
		}
	case "sslport":
		if v, err := strconv.Atoi(value); err == nil {
			o.SslPort = &v
		}
	case "bindaddress":
		s := value
		o.BindAddress = &s
	case "urlbase":
		s := value
		o.URLBase = &s
	case "enablessl":
		if v, err := strconv.ParseBool(value); err == nil {
			o.EnableSsl = &v
		}
	}
}

func applyAuthEnv(o *Overrides, key, value string) {
	switch strings.ToLower(key) {
	case "apikey":
		s := value
		o.APIKey = &s
	case "method":
		s := value
		o.AuthMethod = &s
	case "required":
		s := value
		o.AuthRequired = &s
	}
}

func applyAppEnv(o *Overrides, key, value string) {
	switch strings.ToLower(key) {
	case "instancename":
		s := value
		o.InstanceName = &s
	case "launchbrowser":
		if v, err := strconv.ParseBool(value); err == nil {
			o.LaunchBrowser = &v
		}
	}
}

func applyUpdateEnv(o *Overrides, key, value string) {
	if strings.EqualFold(key, "branch") {
		s := value
		o.BranchOverride = &s
	}
}

// stripPrefixCI returns the suffix and true when s starts with prefix
// under case-insensitive comparison; ("", false) otherwise. Used for
// the `Sonarr__` prefix match — ASP.NET config is case-insensitive,
// so a user can set `SONARR__SERVER__PORT` or `sonarr__server__port`
// and either gets picked up.
func stripPrefixCI(s, prefix string) (string, bool) {
	if len(s) < len(prefix) {
		return "", false
	}
	if !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}
