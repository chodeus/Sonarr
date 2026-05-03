package quality

import (
	"encoding/json"
	"fmt"
)

// DetectionSource records HOW a piece of quality metadata was
// derived during parsing. Mirrors
// `NzbDrone.Core.Qualities.QualityDetectionSource` at
// `QualityDetectionSource.cs:3-9`.
//
// Persisted as the integer enum value inside QualityModel JSON for
// downstream "did this come from the filename or from MediaInfo?"
// decisions in the import pipeline.
type DetectionSource int

const (
	// DetectionUnknown is the default for newly-constructed models;
	// it usually means "not yet inspected".
	DetectionUnknown DetectionSource = 0
	// DetectionName means the quality was inferred from the release
	// title alone (no file inspection).
	DetectionName DetectionSource = 1
	// DetectionExtension means the file extension drove the call
	// (e.g. `.iso` → DVD).
	DetectionExtension DetectionSource = 2
	// DetectionMediaInfo means a MediaInfo probe of the actual file
	// produced the call. Outranks Name and Extension when both
	// disagree.
	DetectionMediaInfo DetectionSource = 3
)

// Model bundles a Quality + Revision plus three "where did this
// come from" detection-source tags. Mirrors
// `NzbDrone.Core.Qualities.QualityModel`.
//
// Persisted as JSON inside columns like `EpisodeFiles.Quality`,
// `Blocklist.Quality`, `History.Quality`. The C# JSON serialiser
// marks the three DetectionSource fields with `[JsonIgnore]` so they
// don't round-trip — they're recomputed each time the model is
// (re)constructed during parsing. The Go port honours that with
// per-field `json:"-"` tags.
//
// Wire shape (per analysis/embedded-docs/catalogue.md § QualityModel):
//
//	{"quality": 7, "revision": {"version":1,"real":0,"isRepack":false}}
//
// `quality` is the BARE Id, not the full Quality object. The C# tree
// applies a per-container `QualityIntConverter` to reduce Quality
// → its Id on serialise. The Go port replicates this via custom
// MarshalJSON / UnmarshalJSON on Model that goes through the
// registry (FindByID).
//
// Equal compares only Quality + Revision (matching the C# Equals at
// `QualityModel.cs:89-102`); the detection-source tags are runtime
// state, not part of identity.
type Model struct {
	Quality                   Quality
	Revision                  Revision
	SourceDetectionSource     DetectionSource
	ResolutionDetectionSource DetectionSource
	RevisionDetectionSource   DetectionSource
}

// modelJSON is the on-the-wire shape. `quality` is a bare int Id;
// `revision` is the nested Revision object. DetectionSource fields
// are absent.
type modelJSON struct {
	Quality  int      `json:"quality"`
	Revision Revision `json:"revision"`
}

// MarshalJSON emits the persisted shape: quality reduced to its Id,
// revision as a nested object. Detection-source fields hidden.
func (m Model) MarshalJSON() ([]byte, error) {
	return json.Marshal(modelJSON{Quality: m.Quality.ID, Revision: m.Revision})
}

// UnmarshalJSON reads the persisted shape, looking up the Quality
// by Id via the registry. An unknown Id deserialises to
// Quality.Unknown (matching `Quality.FindById` C# behaviour for Id 0)
// rather than an error — preserves byte-equivalence on legacy rows
// where Id 11 (retired HDTV480p) appears.
func (m *Model) UnmarshalJSON(data []byte) error {
	var raw modelJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	q, _ := FindByID(raw.Quality)
	m.Quality = q
	m.Revision = raw.Revision
	return nil
}

// NewModel returns a Model wrapping q with a default Revision (v1).
// Mirrors the C# `QualityModel(Quality, Revision = null)` ctor at
// `QualityModel.cs:27-31`.
func NewModel(q Quality) Model {
	return Model{Quality: q, Revision: NewRevision()}
}

// NewModelWithRevision is the explicit-revision constructor.
func NewModelWithRevision(q Quality, r Revision) Model {
	return Model{Quality: q, Revision: r}
}

// String returns the C# `ToString()` shape: `<Quality> <Revision>`
// — e.g. `WEBDL-1080p v1` or `Bluray-2160p v2 Real:1`.
func (m Model) String() string {
	return fmt.Sprintf("%s %s", m.Quality.Name, m.Revision)
}

// Equal reports structural equality on Quality + Revision only.
// DetectionSource fields are runtime state and excluded.
func (m Model) Equal(other Model) bool {
	return m.Quality == other.Quality && m.Revision.Equal(other.Revision)
}

// Compare ranks two Models. Order, matching `QualityModel.cs:50-87`:
//
//  1. By definitionWeight(Quality) ascending — lower weight loses.
//  2. By Revision.Real descending then Revision.Version descending.
//
// definitionWeight is the runtime QualityDefinition.Weight, which
// in C# comes from `Quality.DefaultQualityDefinitions` (a static
// table). The Go port colocates that table with the registry; see
// QualityWeight below.
//
// Returns -1/0/+1.
func (m Model) Compare(other Model) int {
	switch {
	case QualityWeight(m.Quality) > QualityWeight(other.Quality):
		return 1
	case QualityWeight(m.Quality) < QualityWeight(other.Quality):
		return -1
	}
	return m.Revision.Compare(other.Revision)
}

// QualityWeight returns the canonical weight for q from the C#
// `Quality.DefaultQualityDefinitions` table at
// `Quality.cs:156-180`. Used by Model.Compare.
//
// The runtime QualityDefinition rows in the DB can override Weight
// per-user, but Model.Compare uses the static defaults — it's the
// "what would the unconfigured app rank?" baseline that the proper-
// upgrade path uses to decide whether a candidate beats the stored
// file. Quality.cs:53-54 likewise reads from the static
// DefaultQualityDefinitions, not from the DB.
func QualityWeight(q Quality) int {
	w, ok := defaultWeights[q.ID]
	if !ok {
		return 0
	}
	return w
}

// defaultWeights mirrors the Weight column from
// `Quality.DefaultQualityDefinitions` at `Quality.cs:158-179`. Note
// that several qualities share a Weight intentionally (e.g. WEBRip720p
// and WEBDL720p both at 10) — the C# code groups visually-equivalent
// sources so the upgrade path doesn't keep flipping between them.
var defaultWeights = map[int]int{
	Unknown.ID:          1,
	SDTV.ID:             2,
	WEBRip480p.ID:       3,
	WEBDL480p.ID:        3,
	DVD.ID:              4,
	Bluray480p.ID:       5,
	Bluray576p.ID:       6,
	HDTV720p.ID:         7,
	HDTV1080p.ID:        8,
	RawHD.ID:            9,
	WEBRip720p.ID:       10,
	WEBDL720p.ID:        10,
	Bluray720p.ID:       11,
	WEBRip1080p.ID:      12,
	WEBDL1080p.ID:       12,
	Bluray1080p.ID:      13,
	Bluray1080pRemux.ID: 14,
	HDTV2160p.ID:        15,
	WEBRip2160p.ID:      16,
	WEBDL2160p.ID:       16,
	Bluray2160p.ID:      17,
	Bluray2160pRemux.ID: 18,
}
