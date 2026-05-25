package v3

import (
	"fmt"
	"strings"
	"unicode"

	"sonarr.tv/sonarr/internal/quality"
)

// CamelCaseEnumName mirrors `System.Text.Json.JsonNamingPolicy.CamelCase`
// applied to enum identifier strings. Used at the REST edge for fields
// whose Go-side getter returns the persisted PascalCase enum form
// ("Always", "All", "PreferAndUpgrade") but whose C# wire shape uses
// the camelCase form.
//
// Algorithm matches the .NET runtime implementation:
//   - empty input or non-uppercase first char → unchanged
//   - all-uppercase string → all-lowercase ("DVD" → "dvd")
//   - single leading uppercase → lowercase that char ("All" → "all",
//     "PreferAndUpgrade" → "preferAndUpgrade")
//   - multi-char uppercase run before a lowercase tail → lowercase
//     all-but-the-last of the leading run ("IPAddress" → "ipAddress",
//     "TVShow" → "tVShow"? — actually "tvShow", because i-1=1 lowercased
//     leaves the last cap as the word boundary)
//
// Wait the algorithm gives "TVShow" (T+V upper, S+how → lowercase
// "TV"→"tv", keep "Show"): result "tvShow". ✓
func CamelCaseEnumName(name string) string {
	if name == "" {
		return name
	}
	if !unicode.IsUpper(rune(name[0])) {
		return name
	}
	i := 0
	for i < len(name) && unicode.IsUpper(rune(name[i])) {
		i++
	}
	if i == len(name) {
		return strings.ToLower(name)
	}
	if i > 1 {
		return strings.ToLower(name[:i-1]) + name[i-1:]
	}
	return strings.ToLower(name[:1]) + name[1:]
}

// ProperDownloadTypeName maps the persisted quality.ProperDownloadType
// to its C# JsonStringEnumConverter+CamelCase wire form
// ("preferAndUpgrade" / "doNotUpgrade" / "doNotPrefer"). The Go
// quality.ProperDownloadType.String() returns the hyphen-cased form
// for log/internal use; this mapper covers the REST edge.
func ProperDownloadTypeName(p quality.ProperDownloadType) string {
	switch p {
	case quality.ProperPreferAndUpgrade:
		return "preferAndUpgrade"
	case quality.ProperDoNotUpgrade:
		return "doNotUpgrade"
	case quality.ProperDoNotPrefer:
		return "doNotPrefer"
	}
	return "unknown"
}

// AuthenticationMethodFromName reverses CamelCaseEnumName for the
// AuthenticationType enum. Used by /api/v3/config/host PUT (slice
// 5.B13d). "basic" is accepted here so the controller validator can
// emit the friendly "'Basic' is no longer supported, switch to 'Forms'
// instead." message; the AuthenticationMethod setter never sees
// "Basic" because the validator rejects it before persistence.
func AuthenticationMethodFromName(s string) (string, error) {
	switch s {
	case "none":
		return "None", nil
	case "basic":
		return "Basic", nil
	case "forms":
		return "Forms", nil
	case "external":
		return "External", nil
	}
	return "", fmt.Errorf("unknown authenticationMethod value %q (want none|forms|external)", s)
}

// AuthenticationRequiredFromName reverses CamelCaseEnumName for the
// AuthenticationRequiredType enum.
func AuthenticationRequiredFromName(s string) (string, error) {
	switch s {
	case "enabled":
		return "Enabled", nil
	case "disabledForLocalAddresses":
		return "DisabledForLocalAddresses", nil
	case "disabledForLocalhost":
		return "DisabledForLocalhost", nil
	}
	return "", fmt.Errorf("unknown authenticationRequired value %q (want enabled|disabledForLocalAddresses|disabledForLocalhost)", s)
}

// UpdateMechanismFromName reverses CamelCaseEnumName for the
// UpdateMechanism enum.
func UpdateMechanismFromName(s string) (string, error) {
	switch s {
	case "builtIn":
		return "BuiltIn", nil
	case "script":
		return "Script", nil
	case "external":
		return "External", nil
	case "apt":
		return "Apt", nil
	case "docker":
		return "Docker", nil
	}
	return "", fmt.Errorf("unknown updateMechanism value %q (want builtIn|script|external|apt|docker)", s)
}

// ProxyTypeFromName reverses CamelCaseEnumName for the ProxyType enum.
func ProxyTypeFromName(s string) (string, error) {
	switch s {
	case "http":
		return "Http", nil
	case "socks4":
		return "Socks4", nil
	case "socks5":
		return "Socks5", nil
	}
	return "", fmt.Errorf("unknown proxyType value %q (want http|socks4|socks5)", s)
}

// CertificateValidationFromName reverses CamelCaseEnumName for the
// CertificateValidationType enum.
func CertificateValidationFromName(s string) (string, error) {
	switch s {
	case "enabled":
		return "Enabled", nil
	case "disabledForLocalAddresses":
		return "DisabledForLocalAddresses", nil
	case "disabled":
		return "Disabled", nil
	}
	return "", fmt.Errorf("unknown certificateValidation value %q (want enabled|disabledForLocalAddresses|disabled)", s)
}

// ProperDownloadTypeFromName reverses ProperDownloadTypeName. Used by
// the /api/v3/config/mediamanagement PUT handler (slice 5.B13c).
// Unknown values surface as an error rather than defaulting silently
// — the wire shape is a closed enum and a bad value should reject.
func ProperDownloadTypeFromName(s string) (quality.ProperDownloadType, error) {
	switch s {
	case "preferAndUpgrade":
		return quality.ProperPreferAndUpgrade, nil
	case "doNotUpgrade":
		return quality.ProperDoNotUpgrade, nil
	case "doNotPrefer":
		return quality.ProperDoNotPrefer, nil
	}
	return 0, fmt.Errorf("unknown DownloadPropersAndRepacks value %q (want preferAndUpgrade|doNotUpgrade|doNotPrefer)", s)
}
