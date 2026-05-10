// Adapters supporting the slice-3.BL (queue [15])
// *episodefilemoving.ImportScriptService boot wire. The service
// reaches for two narrow read surfaces (instance-name lookup + tag
// label resolution) that the existing host-side singletons
// (configfile.Provider, tags.Service) don't expose in the exact shape
// ImportScriptService needs.

package main

import (
	"context"
	"errors"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/tags"
)

// instanceNameReaderAdapter satisfies
// [episodefilemoving.InstanceNameReader] over the host-side
// *configfile.Provider. The Provider stores InstanceName as a
// regular config-key entry; the adapter calls String(ctx,
// "InstanceName", "Sonarr", false) using a background context (the
// lookup is in-memory after the initial load — no DB hit on the hot
// import path).
type instanceNameReaderAdapter struct {
	p *configfile.Provider
}

// InstanceName returns the resolved instance name, falling back to
// "Sonarr" if the lookup errors (matches the C# default).
func (a instanceNameReaderAdapter) InstanceName() string {
	v, err := a.p.String(context.Background(), "InstanceName", "Sonarr", false)
	if err != nil {
		return "Sonarr"
	}
	return v
}

// tagLabelReaderAdapter satisfies
// [episodefilemoving.TagLabelReader] over *tags.Service. Maps the
// (*Tag, error) shape to (string, ok) — missing rows return ("", false)
// so the env-var build at TryImport can quietly skip unknown IDs
// rather than crash the import.
type tagLabelReaderAdapter struct {
	svc *tags.Service
}

func (a tagLabelReaderAdapter) GetByID(ctx context.Context, id int) (string, bool) {
	t, err := a.svc.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, tags.ErrNotFound) {
			return "", false
		}
		return "", false
	}
	if t == nil {
		return "", false
	}
	return t.Label, true
}
