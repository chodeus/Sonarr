// Package aria2 ports the C# Aria2 download client at
// `src/NzbDrone.Core/Download/Clients/Aria2/`. Aria2 speaks
// XML-RPC over HTTP at the configured `RpcPath` endpoint
// (typically `/rpc`); methods are namespaced `aria2.<verb>` (e.g.
// `aria2.addUri`, `aria2.tellActive`).
//
// The package ships:
//
//   - [Settings] + [NewSettings] + Validate (slice 5.J50 baseline).
//   - XML-RPC [Proxy] + secret-token auth.
//   - Full Provider surface (Test / Download / GetItems / RemoveItem /
//     GetStatus / MarkItemAsImported / GetImportItem / RequestAction)
//     functionally complete vs C# Aria2.cs. Test gates on minVersion
//     1.34.0 (slice J110 alignment with C# :237); RemoveItem
//     dispatches to forceRemove (active) or removeDownloadResult
//     (complete/error/removed) and wires the deleteData arm via
//     [downloadclients.DiskProvider] (slice J112 — aria2's own API
//     can't delete files per upstream issue #728).
//   - init() registers `(Aria2, Aria2Settings)` in
//     [downloadclients.Default].
//
// C# cites lead each type with file:line references.
package aria2

import (
	"errors"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
)

// Settings ports `Aria2Settings` at
// `src/NzbDrone.Core/Download/Clients/Aria2/Aria2Settings.cs:15-51`.
//
// Defaults mirror the C# constructor at `:19-26`: Host=localhost,
// Port=6800, RpcPath="/rpc", UseSsl=false,
// SecretToken="MySecretToken".
//
// The C# validator at `:7-13` only checks Host (ValidHost) — Aria2
// is permissive about port + path. The Go port additionally pins
// Port to the 1..65535 range to catch obvious mis-configuration
// before the network probe.
type Settings struct {
	downloadclients.BaseTorrentSettings

	// Host is the aria2c RPC host. C# `:29`.
	Host string `json:"host"`

	// Port is the aria2c RPC port (default 6800). C# `:32`.
	Port int `json:"port"`

	// RpcPath is the URL path the aria2c RPC endpoint lives at
	// (default "/rpc"). C# `:35`. Trailing-slash is normalised at
	// the proxy boundary.
	RpcPath string `json:"rpcPath"`

	// UseSsl flips http→https. C# `:38`.
	UseSsl bool `json:"useSsl"`

	// SecretToken is the RPC shared secret. C# `:41`. Prepended to
	// every method call args as `"token:<value>"` per aria2c's
	// `--rpc-secret` auth convention.
	SecretToken string `json:"secretToken"`

	// Directory is the optional explicit download directory the
	// proxy sets via the `dir` option on add. Empty leaves the
	// aria2c configuration default. C# `:44`.
	Directory string `json:"directory"`
}

// NewSettings returns a Settings with the C# constructor defaults
// applied.
func NewSettings() Settings {
	return Settings{
		Host:        "localhost",
		Port:        6800,
		RpcPath:     "/rpc",
		UseSsl:      false,
		SecretToken: "MySecretToken",
	}
}

// Validate ports `Aria2SettingsValidator` at C# `:7-13` + the Go
// port's added Port range check.
func (s Settings) Validate() []downloadclients.TestFailure {
	var failures []downloadclients.TestFailure
	if strings.TrimSpace(s.Host) == "" {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Host",
			ErrorMessage: "Host is required",
		})
	}
	if s.Port < 1 || s.Port > 65535 {
		failures = append(failures, downloadclients.TestFailure{
			PropertyName: "Port",
			ErrorMessage: "Port must be between 1 and 65535",
		})
	}
	return failures
}

// ErrSettingsTypeMismatch is returned when the constructor receives
// a Settings JSON body that doesn't decode cleanly.
var ErrSettingsTypeMismatch = errors.New("aria2: settings type mismatch")
