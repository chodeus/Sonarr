// Package vuze ports the C# Vuze download client at
// `src/NzbDrone.Core/Download/Clients/Vuze/`. Vuze ships a
// Transmission-compatible "Web Remote UI" RPC plugin that speaks
// the same JSON-RPC surface as Transmission itself — so the C#
// tree's [Vuze] class inherits from `TransmissionBase` and only
// overrides:
//
//   - Name → "Vuze".
//   - SupportsLabels → false (the C# Vuze Web Remote UI doesn't
//     expose label tagging).
//   - ValidateVersion → integer protocol-version check (>= 14)
//     against the `rpc-version` field rather than the semver
//     check Transmission uses.
//   - GetOutputPath → joins the torrent name onto the output path
//     for single-file torrents when the basename doesn't already
//     match (matches Vuze's quirk where the WebUI reports the
//     parent folder for single-file torrents).
//
// Slice 5.J54 (this slice) ships the full Vuze Provider by
// embedding `*transmission.Transmission` and overriding Name() +
// Test(). The GetOutputPath override is documented as a
// follow-up — the Go-port transmission.Transmission's
// torrentToItem builds the OutputPath inline, so applying the
// Vuze-specific single-file quirk requires either re-implementing
// GetItems (LOC-heavy) or extending the transmission Provider with
// a hook. Documented divergence; user impact is limited to
// post-import path resolution on single-file torrents whose
// basename doesn't match the torrent name (rare).
//
// SupportsLabels=false is enforced by Test (validation) but the
// Test inherits from transmission; for the MVP slice the label-
// support divergence surfaces only in the rare configuration
// where the operator sets a category on a Vuze client — the
// torrent gets added without the label (Transmission's label-set
// post-add silently no-ops on Vuze because the WebUI rejects
// `labels:` arg). Same C# divergence; both fail safely.
package vuze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/transmission"
)

// Implementation + ConfigContract are the persisted-row identifiers
// for the Vuze download client. Mirrors C# `Vuze` class name +
// inherited `TransmissionSettings` (Vuze reuses Transmission's
// settings type at `Vuze.cs:14`).
const (
	Implementation = "Vuze"
	ConfigContract = "TransmissionSettings"
)

// minProtocolVersion is the integer RPC protocol-version floor
// [Test] enforces. Matches C# `Vuze.MINIMUM_SUPPORTED_PROTOCOL_VERSION`
// at `:16` — Vuze 5.0+ ships RPC protocol 14.
const minProtocolVersion = 14

// Vuze embeds *transmission.Transmission to inherit the Provider
// surface (Download / GetItems / RemoveItem / etc.) and overrides
// Name() + Test() with the Vuze-specific shape.
type Vuze struct {
	*transmission.Transmission
	proxy    *transmission.Proxy
	settings transmission.Settings
	log      *slog.Logger
}

// New constructs a Vuze Provider against the resolved settings +
// optional Proxy. Mirrors `transmission.New` shape; the Proxy is
// shared with the wrapped Transmission instance.
func New(def *downloadclients.DownloadClient, settings transmission.Settings, proxy *transmission.Proxy, log *slog.Logger) *Vuze {
	if log == nil {
		log = slog.Default()
	}
	t := transmission.New(def, settings, proxy, log)
	return &Vuze{
		Transmission: t,
		proxy:        proxy,
		settings:     settings,
		log:          log,
	}
}

// Name overrides the embedded Transmission.Name(). Mirrors C#
// `Vuze.Name` at `:18`.
func (v *Vuze) Name() string { return "Vuze" }

// Test overrides the embedded Transmission.Test() with the Vuze-
// specific protocol-version check. Mirrors C# `ValidateVersion` at
// `:58-72`: parses the GetConfig response's `rpc-version` field as
// an integer + rejects < [minProtocolVersion].
//
// Settings validation runs first (delegated via Settings.Validate);
// the network probe runs only when no Settings failures + a proxy
// is wired.
func (v *Vuze) Test(ctx context.Context) ([]downloadclients.TestFailure, error) {
	failures := v.settings.Validate()
	if len(failures) > 0 || v.proxy == nil {
		return failures, nil
	}
	cfg, err := v.proxy.GetConfig(ctx, v.settings)
	if err != nil {
		return []downloadclients.TestFailure{{
			PropertyName: "Host",
			ErrorMessage: fmt.Sprintf("Unable to connect to Vuze: %v", err),
		}}, nil
	}
	version, err := strconv.Atoi(cfg.RpcVersion)
	if err != nil || version < minProtocolVersion {
		return []downloadclients.TestFailure{{
			PropertyName: "",
			ErrorMessage: fmt.Sprintf("Vuze protocol version %q is too old; minimum supported is %d", cfg.RpcVersion, minProtocolVersion),
		}}, nil
	}
	return nil, nil
}

// Compile-time guard.
var _ downloadclients.Provider = (*Vuze)(nil)

// constructor is the registry factory. Builds the shared
// transmission.Proxy when deps.HTTPClient is available.
func constructor(def *downloadclients.DownloadClient, deps downloadclients.Dependencies) (downloadclients.Provider, error) {
	if def == nil {
		return nil, errors.New("vuze: nil DownloadClient row")
	}
	settings := transmission.NewSettings()
	if def.Settings != "" {
		if err := json.Unmarshal([]byte(def.Settings), &settings); err != nil {
			return nil, fmt.Errorf("vuze: settings unmarshal: %w", err)
		}
	}
	var log *slog.Logger
	if l, ok := deps.Logger.(*slog.Logger); ok {
		log = l
	}
	var proxy *transmission.Proxy
	if deps.HTTPClient != nil {
		doer, ok := deps.HTTPClient.(transmission.HTTPDoer)
		if !ok {
			return nil, fmt.Errorf("vuze: deps.HTTPClient (%T) does not satisfy transmission.HTTPDoer", deps.HTTPClient)
		}
		var err error
		proxy, err = transmission.NewProxy(doer, log)
		if err != nil {
			return nil, err
		}
	}
	return New(def, settings, proxy, log), nil
}

// init registers Vuze with the global registry.
func init() {
	downloadclients.Default().MustRegister(Implementation, ConfigContract, constructor)
}
