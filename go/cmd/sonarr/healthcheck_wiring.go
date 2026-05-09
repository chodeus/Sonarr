// Boot-time adapters that bridge the production services into the
// `internal/healthcheck` per-check narrow interfaces. Same posture as
// `decisionengine_wiring.go` / `downloadedimport_wiring.go`: the
// adapter only exists where a real service exists but its surface
// differs from what a check needs.
//
// The healthcheck.Service check list is fixed at construction time
// (see [healthcheck.NewService]); each new check requires a boot-site
// constructor invocation. Adapters live here so main.go's serve()
// stays focused on ordering / dependency wiring.

package main

import (
	"context"

	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/namingconfig"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// seriesFolderFormatAdapter bridges *namingconfig.Service to the
// narrow [rootfolders.NamingConfigProvider] which only needs the
// SeriesFolderFormat field. Pulls the singleton row via Service.GetConfig
// (cached per slice 3.A2) and forwards the one field.
//
// Errors propagate verbatim. nil-Config arm is defensive only — the
// Service guarantees a non-nil row whenever GetConfig returns nil err.
type seriesFolderFormatAdapter struct {
	svc *namingconfig.Service
}

func (a seriesFolderFormatAdapter) GetSeriesFolderFormat(ctx context.Context) (string, error) {
	cfg, err := a.svc.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", nil
	}
	return cfg.SeriesFolderFormat, nil
}

// Compile-time guards.
var (
	_ rootfolders.NamingConfigProvider = seriesFolderFormatAdapter{}
	_ healthcheck.RootFolderResolver   = (*rootfolders.Service)(nil)
)
