package series

import (
	"encoding/json"
	"strings"
)

// MonitorType ports the C# `MonitorTypes` enum at
// `src/NzbDrone.Core/Tv/MonitoringOptions.cs:13-30`. Persisted inside
// the per-row AddOptions JSON blob via the C# `JsonStringEnumConverter
// (JsonNamingPolicy.CamelCase, allowIntegerValues: true)` registered
// at `Datastore/Converters/EmbeddedDocumentConverter.cs:25` — so the
// wire form can be the camelCase enum name ("none", "all", "future",
// etc.) OR the integer ordinal. The Go-port decoder accepts both.
//
// Integer values are frozen — they match the C# enum's declaration
// order (Unknown=0, All=1, ..., Skip=14). Persistence-side
// compatibility on read AND write depends on these values; renumbering
// would break every embedded AddOptions blob already on disk.
type MonitorType int

// MonitorType ordinals — frozen, matching C# MonitorTypes declaration order.
const (
	MonitorUnknown           MonitorType = 0
	MonitorAll               MonitorType = 1
	MonitorFuture            MonitorType = 2
	MonitorMissing           MonitorType = 3
	MonitorExisting          MonitorType = 4
	MonitorFirstSeason       MonitorType = 5
	MonitorLastSeason        MonitorType = 6
	MonitorLatestSeason      MonitorType = 7 // C# [Obsolete]; preserved for read-back compat.
	MonitorPilot             MonitorType = 8
	MonitorRecent            MonitorType = 9
	MonitorMonitorSpecials   MonitorType = 10
	MonitorUnmonitorSpecials MonitorType = 11
	MonitorNone              MonitorType = 12
	MonitorSkip              MonitorType = 13
)

// AddOptions is the decoded shape of the per-row `Series.AddOptions`
// JSON blob. Ports the C# `AddSeriesOptions : MonitoringOptions`
// composite at `Tv/AddSeriesOptions.cs:3-7` + `Tv/MonitoringOptions.cs:
// 5-10`. Only the three fields used by the Phase-5 callers are mapped;
// IgnoreEpisodesWithFiles + IgnoreEpisodesWithoutFiles (consumed by
// RefreshSeriesService when seeding episode-level monitored state)
// can be added in the follow-up RefreshSeries slice.
type AddOptions struct {
	// Monitor is the MonitorType the C# UI sent. Wire-form can be a
	// string ("none", "all", etc.) OR an integer (12, 1, etc.); the
	// UnmarshalJSON method handles both.
	Monitor MonitorType `json:"monitor"`

	// SearchForMissingEpisodes mirrors C# `AddSeriesOptions
	// .SearchForMissingEpisodes` at `:5`. Consumed by the post-
	// AddSeries search trigger (deferred slice).
	SearchForMissingEpisodes bool `json:"searchForMissingEpisodes"`

	// SearchForCutoffUnmetEpisodes mirrors C# `:6`. Same posture.
	SearchForCutoffUnmetEpisodes bool `json:"searchForCutoffUnmetEpisodes"`
}

// UnmarshalJSON accepts both the integer ordinal and the camelCase
// enum name for the Monitor field, matching the C#
// `JsonStringEnumConverter(JsonNamingPolicy.CamelCase, true)` shape.
// Unknown names decode to [MonitorUnknown] rather than erroring — keeps
// the import-list pipeline resilient against future C# enum
// additions that the Go-port hasn't backported yet.
func (m *MonitorType) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*m = MonitorUnknown
		return nil
	}
	// Numeric form — any int value within [0, 13] is a valid enum
	// ordinal. Out-of-range numerics decode to Unknown.
	if trimmed[0] != '"' {
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		*m = MonitorType(n)
		return nil
	}
	// String form — case-insensitive name lookup mirrors C#
	// JsonStringEnumConverter's case-insensitive matching (its second
	// argument `true` enables case-insensitive read).
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*m = ParseMonitorTypeName(s)
	return nil
}

// ParseMonitorTypeName parses a C# MonitorTypes enum-identifier string
// (case-insensitive) into the typed enum. Unknown / empty input
// returns [MonitorUnknown]. Exported so external packages (e.g.
// `internal/api/v3` which converts wire-form camelCase strings) can
// reuse the same parser the AddOptions UnmarshalJSON uses.
func ParseMonitorTypeName(s string) MonitorType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "unknown":
		return MonitorUnknown
	case "all":
		return MonitorAll
	case "future":
		return MonitorFuture
	case "missing":
		return MonitorMissing
	case "existing":
		return MonitorExisting
	case "firstseason":
		return MonitorFirstSeason
	case "lastseason":
		return MonitorLastSeason
	case "latestseason":
		return MonitorLatestSeason
	case "pilot":
		return MonitorPilot
	case "recent":
		return MonitorRecent
	case "monitorspecials":
		return MonitorMonitorSpecials
	case "unmonitorspecials":
		return MonitorUnmonitorSpecials
	case "none":
		return MonitorNone
	case "skip":
		return MonitorSkip
	default:
		return MonitorUnknown
	}
}

// DecodeAddOptions parses the JSON-blob form of Series.AddOptions.
// Empty / whitespace-only / "null" input returns (nil, nil) —
// matches the C# `newSeries.AddOptions != null` guard at
// `AddSeriesService.cs:154` treating a missing options object as
// "no override". Malformed JSON returns an error so the caller can
// log + continue (the typical posture is warn + treat as nil).
func DecodeAddOptions(blob string) (*AddOptions, error) {
	trimmed := strings.TrimSpace(blob)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var opts AddOptions
	if err := json.Unmarshal([]byte(trimmed), &opts); err != nil {
		return nil, err
	}
	return &opts, nil
}
