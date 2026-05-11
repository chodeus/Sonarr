package sonarr

import "sonarr.tv/sonarr/internal/importlists"

// init registers the SonarrImport constructor with the
// package-global [importlists.Default] registry. The blank-import
// aggregator in cmd/sonarr/main.go pulls this package in at
// startup so the registration fires before the orchestrator
// (queue [I4]) calls Registry.Construct.
//
// Mirrors the C# DI auto-discovery — Sonarr's ServiceFactory finds
// every `IImportList` implementation in the assembly at startup
// and registers them by Type.Name + ConfigContract type. The Go
// port replaces reflection-based discovery with init()-time
// explicit registration.
func init() {
	importlists.Default().MustRegister(Implementation, ConfigContract, New)
}
