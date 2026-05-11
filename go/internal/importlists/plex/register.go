package plex

import "sonarr.tv/sonarr/internal/importlists"

// init registers the PlexImport constructor with the
// package-global [importlists.Default] registry. The blank-import
// aggregator in cmd/sonarr/main.go pulls this package in at
// startup so the registration fires before the orchestrator
// (queue [I4]) calls Registry.Construct.
//
// Pattern-mirror of internal/importlists/sonarr/register.go.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
