package main

import (
	"context"
	"fmt"
	"log/slog"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/config"
	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/language"
)

// buildHostServer assembles the *host.Server from the runtime
// dependencies serve() already has in scope. Lives in its own file
// so main.go's wiring block stays scannable as Phase 5 grows.
//
// Mirrors `src/NzbDrone.Host/Bootstrap.cs:CreateConsoleHostBuilder`
// where BindAddress + Port are pulled from the configuration before
// the URL list is handed to Kestrel.
func buildHostServer(ctx context.Context, cfg *configfile.Provider, logger *slog.Logger, configRepo *config.Repo) (*host.Server, error) {
	bind, err := cfg.BindAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: bind address: %w", err)
	}
	port, err := cfg.Port(ctx)
	if err != nil {
		return nil, fmt.Errorf("host: port: %w", err)
	}
	return host.New(
		host.Config{BindAddress: bind, Port: port},
		logger,
		configRepoProbe{configRepo},
		// *configfile.Provider satisfies host.APIKeyResolver via
		// its APIKey(ctx) (string, error) method — no adapter
		// needed.
		cfg,
		host.WithRoutes(v3Routes()...),
	), nil
}

// v3Routes collects the v3 controllers wired in this slice. As more
// controllers port, they're added here in alphabetical order. The
// language registry is constant (every Language is a package-level
// var in internal/language), so the slice is captured once at boot.
func v3Routes() []host.Route {
	langs := language.All()
	return []host.Route{
		{Method: "GET", Path: "/api/v3/language", Handler: apiv3.LanguageHandler(langs)},
	}
}

// configRepoProbe adapts *config.Repo to host.ConfigProbe. The
// C# PingController calls `_configRepository.All()` purely for its
// side-effect (raise on DB failure); the Go probe collapses that
// to a single-bool error signal.
type configRepoProbe struct{ r *config.Repo }

func (p configRepoProbe) All(ctx context.Context) error {
	_, err := p.r.All(ctx)
	return err
}
