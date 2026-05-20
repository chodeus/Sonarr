package aria2

import (
	"strconv"
	"strings"
)

// versionPair is the (major, minor) pair the Test version-floor
// gate consumes. Same idiom as rtorrent's version helper.
type versionPair struct {
	major int
	minor int
}

// parseVersion extracts the leading (major, minor) from a version
// string. Tolerates "1.36.0", "1.36.0-something", "v1.36.0".
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

// versionAtLeast reports whether `actual` is ≥ `floor`.
func versionAtLeast(actual string, floor versionPair) bool {
	v := parseVersion(actual)
	if v.major != floor.major {
		return v.major > floor.major
	}
	return v.minor >= floor.minor
}
