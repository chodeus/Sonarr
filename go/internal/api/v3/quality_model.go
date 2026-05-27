package v3

import "sonarr.tv/sonarr/internal/quality"

// QualityValue is the wire-shape mirror of
// `NzbDrone.Core.Qualities.Quality` (`src/NzbDrone.Core/Qualities/
// Quality.cs:10-16`) — the 4-field DTO carried as the inner
// `quality` member of QualityModelResource. Distinct from
// [quality.Quality] (the persistence struct with same shape but no
// camelCase Source wire conversion).
//
// Wire shape: `{"id":N,"name":"X","source":"television","resolution":N}`.
// `source` is wired as camelCase string per STJson's
// `JsonStringEnumConverter(CamelCase)` applied to the C# QualitySource
// enum (e.g. "television" / "televisionRaw" / "web" / "webRip" / "dvd"
// / "bluray" / "blurayRaw" / "unknown"). The Go-port shares the
// camelCase mapping via [QualitySourceName].
//
// All fields no-omitempty (always emit when the parent struct is
// non-nil) — matches the C# default-non-null property serialization
// once the parent QualityModel is populated.
//
// J325.
type QualityValue struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Resolution int    `json:"resolution"`
}

// RevisionValue is the wire-shape mirror of
// `NzbDrone.Core.Qualities.Revision` (`src/NzbDrone.Core/Qualities/
// Revision.cs:6-21`) — the 3-field DTO carried as the inner
// `revision` member of QualityModelResource. Distinct from
// [quality.Revision] (the persistence struct with same shape; the
// fields here re-use the wire-tagged form for the wrapping DTO).
//
// Wire shape: `{"version":N,"real":N,"isRepack":bool}`. All fields
// no-omitempty (always emit when the parent struct is non-nil) —
// matches C# default-non-null serialization.
//
// J325.
type RevisionValue struct {
	Version  int  `json:"version"`
	Real     int  `json:"real"`
	IsRepack bool `json:"isRepack"`
}

// QualityModelResource is the wire-shape mirror of
// `NzbDrone.Core.Qualities.QualityModel` (`src/NzbDrone.Core/Qualities/
// QualityModel.cs:9-15`) — the 2-field envelope wrapping a Quality +
// Revision pair. Surfaced as `*QualityModelResource` on carrier
// resources (HistoryResource, BlocklistResource, QueueResource,
// ManualImportReprocessItem in C#) with omitempty so nil drops the
// key matching `QualityModel?` null under WhenWritingNull.
//
// Wire shape:
//
//	{
//	  "quality":  {"id":N,"name":"X","source":"...","resolution":N},
//	  "revision": {"version":N,"real":N,"isRepack":bool}
//	}
//
// Note on the persistence-vs-wire divergence: the persisted
// [quality.Model] has a custom MarshalJSON that emits the reduced
// form `{"quality":<int-id>,"revision":{...}}` matching the C#
// `QualityIntConverter` posture for IEmbeddedDocument storage.
// QualityModelResource is the expanded form for the REST API
// surface — the populated mapper allocates via [ToQualityModelResource]
// at projection time.
//
// J325 — wire-shape pin + ToQualityModelResource helper. Production
// emit-site population is deferred to the per-family populated-row
// mapper port (history.Service paged, blocklist.Service paged,
// queue.Service paged).
type QualityModelResource struct {
	Quality  QualityValue  `json:"quality"`
	Revision RevisionValue `json:"revision"`
}

// ToQualityModelResource projects a persisted [quality.Model] into
// the expanded wire shape. Uses [QualitySourceName] for the
// camelCase Source enum-string conversion. Pointer-returning so
// nil input is safe at the call site (the populated-mapper short-
// circuits on zero-value Quality.Unknown — C# null-out under
// WhenWritingNull).
//
// Pointer return is `*QualityModelResource` so the caller can
// assign directly to a carrier's `Quality *QualityModelResource`
// field. A nil return drops the wire key entirely.
//
// J325.
func ToQualityModelResource(m quality.Model) *QualityModelResource {
	return &QualityModelResource{
		Quality: QualityValue{
			ID:         m.Quality.ID,
			Name:       m.Quality.Name,
			Source:     QualitySourceName(m.Quality.Source),
			Resolution: m.Quality.Resolution,
		},
		Revision: RevisionValue{
			Version:  m.Revision.Version,
			Real:     m.Revision.Real,
			IsRepack: m.Revision.IsRepack,
		},
	}
}
