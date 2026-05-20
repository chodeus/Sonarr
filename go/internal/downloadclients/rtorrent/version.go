package rtorrent

import (
	"strconv"
	"strings"
)

// versionPair is the (major, minor) pair the Test version-floor
// gate consumes. Matches the qBittorrent-package's identical idiom.
type versionPair struct {
	major int
	minor int
}

// parseVersion extracts the leading (major, minor) from a version
// string. Tolerates "0.9.8", "0.9.8-something", "v0.9.8". Returns
// zero on parse failure — the Test caller treats (0,0) as
// definitely-too-old, matching the C# `Version.TryParse` pattern.
func parseVersion(s string) versionPair {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return versionPair{}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return versionPair{}
	}
	// Some builds emit a minor like "9-rc1" — strip the suffix.
	minorPart := parts[1]
	for i, r := range minorPart {
		if r < '0' || r > '9' {
			minorPart = minorPart[:i]
			break
		}
	}
	minor, err := strconv.Atoi(minorPart)
	if err != nil {
		return versionPair{}
	}
	return versionPair{major: major, minor: minor}
}

// versionAtLeast reports whether `actual` is ≥ `floor`. Major
// dominates; ties on major fall through to minor.
func versionAtLeast(actual string, floor versionPair) bool {
	v := parseVersion(actual)
	if v.major != floor.major {
		return v.major > floor.major
	}
	return v.minor >= floor.minor
}
