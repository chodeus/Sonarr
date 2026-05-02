// Package buildinfo exposes build-time identity for the Sonarr binary.
//
// Values are populated at link time via -ldflags="-X sonarr.tv/sonarr/internal/buildinfo.Version=..."
// from the CI build. Defaults below are used in dev builds.
package buildinfo

import "fmt"

// Version is the semantic version of this build (e.g. "5.0.0.1234").
var Version = "dev"

// Commit is the short git commit hash of this build.
var Commit = "unknown"

// BuildDate is the RFC3339 UTC timestamp the binary was built.
var BuildDate = "unknown"

// String returns a human-readable build identity line.
func String() string {
	return fmt.Sprintf("sonarr %s (commit %s, built %s)", Version, Commit, BuildDate)
}
