package quality

import (
	"fmt"
	"strings"
)

// Revision tracks the proper / repack metadata for a release. Mirrors
// `NzbDrone.Core.Qualities.Revision`. Three fields:
//
//   - Version: release iteration. New releases start at 1; a PROPER
//     bumps to 2, another PROPER to 3, and so on. Higher Version
//     wins in ranking when Real is tied.
//   - Real: REAL.PROPER count. Higher Real wins outright over a
//     plain PROPER (any Real > 0 supersedes any Version).
//   - IsRepack: a "repack" annotation that's tracked but does not
//     participate in ordering — propagated through the system so
//     the renamer can label files correctly.
//
// Compare returns -1/0/+1 with the C# semantics: Real first, then
// Version. Equality is structural over (Version, Real) — IsRepack is
// deliberately NOT part of equality, matching the C# Equals impl
// at `Revision.cs:23-31`.
type Revision struct {
	Version  int  `json:"version"`
	Real     int  `json:"real"`
	IsRepack bool `json:"isRepack"`
}

// NewRevision returns a Revision with the C# default Version=1, Real=0.
// The C# parameterless ctor at `Revision.cs:8-10` is private and only
// used by the JSON deserialiser; the parameterised ctor at
// `Revision.cs:12-17` defaults to (1, 0, false). The Go port collapses
// to one constructor with sensible defaults.
func NewRevision() Revision { return Revision{Version: 1} }

// Compare returns -1 if r < other, +1 if r > other, 0 if equal.
// Real takes precedence over Version (matches `Revision.cs:33-56`).
func (r Revision) Compare(other Revision) int {
	switch {
	case r.Real > other.Real:
		return 1
	case r.Real < other.Real:
		return -1
	case r.Version > other.Version:
		return 1
	case r.Version < other.Version:
		return -1
	}
	return 0
}

// Equal reports structural equality on (Version, Real). IsRepack is
// not compared, matching `Revision.cs:23-31`.
func (r Revision) Equal(other Revision) bool {
	return r.Version == other.Version && r.Real == other.Real
}

// String returns the C# `ToString()` shape: `v<Version>` plus
// ` Real:<Real>` when Real > 0. Used in log lines and error messages
// where readable revision shorthand is expected.
func (r Revision) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "v%d", r.Version)
	if r.Real > 0 {
		fmt.Fprintf(&b, " Real:%d", r.Real)
	}
	return b.String()
}
