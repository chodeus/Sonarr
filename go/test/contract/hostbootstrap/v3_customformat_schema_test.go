package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3CustomFormatSchemaContract pins /api/v3/customformat/schema
// to the C#-recorded golden for the 8 C# specification schemas
// (ordered by `ICustomFormatSpecification.Order` ascending) with the
// ReleaseTitleSpecification carrying its 6 hard-coded regex presets,
// PLUS 3 Go-only MediaInfo-based conditions appended at the end
// (HdrFormatSpecification, AudioCodecSpecification,
// AudioChannelsSpecification — Slice A forward-divergence; see
// internal/customformats/spec_mediainfo.go). The golden's first 8
// entries remain byte-identical to the C# recording; the 3 trailing
// entries are the documented intentional divergence (current C# has no
// MediaInfo CF specs — tracks Sonarr/Sonarr#5598).
//
// The Go side substitutes a reflection-derived schema with a hand-
// rolled `customFormatSchemas` table — same wire shape, no C#
// FieldDefinition attribute reflection in the Go port today. User-
// defined preset rows from persisted CustomFormat conditions are
// DEFERRED with the customformat list-import sub-arc; the recording
// corpus is an empty DB so the merge yields no extras.
func TestV3CustomFormatSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.customformat.schema"
	checkEmptyJSONContract(t, entryID, apiv3.CustomFormatSchemaHandler())
}
