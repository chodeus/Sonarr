// Package quality ports `NzbDrone.Core.Qualities.Quality` plus the
// QualitySource enum that classifies each quality. The Quality
// registry is a small fixed table: 22 named qualities with frozen
// integer Ids that persist in user databases (qualityprofiles,
// episodefiles, history, blocklist, customformats). Renumbering would
// corrupt rows.
//
// QualityDefinition (per-quality min/max/preferred MB-per-minute
// thresholds) lives in `internal/qualitydefinitions` (already shipped
// as a model repo). This package only carries the static taxonomy —
// the dynamic per-user thresholds are loaded from the DB at runtime.
package quality

// QualitySource classifies the origin/source-medium of a Quality
// entry. Values are integer-stable in JSON-embedded fields (e.g.
// CustomFormat SourceSpecification.value), so changes here would
// break round-trips with existing user databases.
type QualitySource int

// Predefined QualitySource values mirroring the C# enum at
// `QualitySource.cs:3-13`. Integer order is the canonical wire form
// inside CustomFormat SourceSpecification.value.
const (
	SourceUnknown       QualitySource = 0
	SourceTelevision    QualitySource = 1
	SourceTelevisionRaw QualitySource = 2
	SourceWeb           QualitySource = 3
	SourceWebRip        QualitySource = 4
	SourceDVD           QualitySource = 5
	SourceBluray        QualitySource = 6
	SourceBlurayRaw     QualitySource = 7
)

// Quality is one entry in the quality taxonomy. Rows reference it by
// Id; Name is for display; Source classifies the medium; Resolution
// is the height in pixels (480 / 576 / 720 / 1080 / 2160).
//
// JSON tags use camelCase to match the C# embedded-doc serializer
// profile (analysis/embedded-docs/catalogue.md § "JSON serialiser
// profile"). The full-form Quality JSON is used by REST endpoints
// only — when persisted inside QualityModel, Quality is reduced to
// its bare Id via Model.MarshalJSON.
//
// NOTE: the Source field marshals as the int enum value, not the
// C# `JsonStringEnumConverter` camelCase string ("television" etc.).
// Phase 2 ships the integer wire form because no Phase 2 consumer
// reads or emits the full-form Quality yet — REST is Phase 5 and
// will swap in a string marshaller. Until then, deserialising a
// REST-style Quality JSON with `"source":"television"` will fail;
// the persisted-model path uses int-reduction so this is fine for
// the current scope.
type Quality struct {
	ID         int           `json:"id"`
	Name       string        `json:"name"`
	Source     QualitySource `json:"source"`
	Resolution int           `json:"resolution"`
}

// Predefined Quality values. IDs are frozen — they are persisted in
// user databases and cannot be reordered. Order in source mirrors
// `Quality.cs:77-124` (the static getters) so a future drift against
// the C# tree is line-aligned.
//
// Note Id 11 is intentionally skipped — `HDTV480p` was retired (see
// `Quality.cs:89` comment "// public static Quality HDTV480p"). Don't
// repurpose it.
var (
	Unknown          = Quality{0, "Unknown", SourceUnknown, 0}
	SDTV             = Quality{1, "SDTV", SourceTelevision, 480}
	DVD              = Quality{2, "DVD", SourceDVD, 480}
	WEBDL1080p       = Quality{3, "WEBDL-1080p", SourceWeb, 1080}
	HDTV720p         = Quality{4, "HDTV-720p", SourceTelevision, 720}
	WEBDL720p        = Quality{5, "WEBDL-720p", SourceWeb, 720}
	Bluray720p       = Quality{6, "Bluray-720p", SourceBluray, 720}
	Bluray1080p      = Quality{7, "Bluray-1080p", SourceBluray, 1080}
	WEBDL480p        = Quality{8, "WEBDL-480p", SourceWeb, 480}
	HDTV1080p        = Quality{9, "HDTV-1080p", SourceTelevision, 1080}
	RawHD            = Quality{10, "Raw-HD", SourceTelevisionRaw, 1080}
	WEBRip480p       = Quality{12, "WEBRip-480p", SourceWebRip, 480}
	Bluray480p       = Quality{13, "Bluray-480p", SourceBluray, 480}
	WEBRip720p       = Quality{14, "WEBRip-720p", SourceWebRip, 720}
	WEBRip1080p      = Quality{15, "WEBRip-1080p", SourceWebRip, 1080}
	HDTV2160p        = Quality{16, "HDTV-2160p", SourceTelevision, 2160}
	WEBRip2160p      = Quality{17, "WEBRip-2160p", SourceWebRip, 2160}
	WEBDL2160p       = Quality{18, "WEBDL-2160p", SourceWeb, 2160}
	Bluray2160p      = Quality{19, "Bluray-2160p", SourceBluray, 2160}
	Bluray1080pRemux = Quality{20, "Bluray-1080p Remux", SourceBlurayRaw, 1080}
	Bluray2160pRemux = Quality{21, "Bluray-2160p Remux", SourceBlurayRaw, 2160}
	Bluray576p       = Quality{22, "Bluray-576p", SourceBluray, 576}
)

// All returns every Quality in registration order. Mirrors
// `Quality.All` in the C# tree (the order in the static ctor at
// `Quality.cs:128-152`, which differs from the source-declaration
// order). Returned slice is a fresh copy so callers cannot mutate
// the registry.
func All() []Quality {
	out := make([]Quality, len(allQualities))
	copy(out, allQualities)
	return out
}

// allQualities mirrors the C# `Quality.All` initialisation order,
// which is the order qualities appear in the UI / quality profile
// editor — NOT the source-declaration order. Test pins this so a
// future drift fails loudly.
var allQualities = []Quality{
	Unknown,
	SDTV,
	DVD,
	WEBRip480p,
	WEBDL480p,
	Bluray480p,
	Bluray576p,
	HDTV720p,
	WEBRip720p,
	WEBDL720p,
	Bluray720p,
	Bluray1080p,
	HDTV1080p,
	WEBRip1080p,
	WEBDL1080p,
	RawHD,
	HDTV2160p,
	WEBRip2160p,
	WEBDL2160p,
	Bluray2160p,
	Bluray1080pRemux,
	Bluray2160pRemux,
}

var byID = func() map[int]Quality {
	m := make(map[int]Quality, len(allQualities))
	for _, q := range allQualities {
		m[q.ID] = q
	}
	return m
}()

// FindByID returns the Quality with the given Id and whether it
// matched. Id 0 always resolves to Unknown (matches the C# guard at
// `Quality.cs:191`); unknown ids return (Unknown, false).
//
// The C# tree throws on unknown ids (`ArgumentException`); the Go
// port returns ok=false instead. Callers that want the C# panic
// semantics can wrap.
func FindByID(id int) (Quality, bool) {
	if id == 0 {
		return Unknown, true
	}
	q, ok := byID[id]
	if !ok {
		return Unknown, false
	}
	return q, true
}
