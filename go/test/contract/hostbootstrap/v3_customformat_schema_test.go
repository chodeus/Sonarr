package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3CustomFormatSchemaContract pins /api/v3/customformat/schema
// to the C#-recorded golden — 8 specification schemas (ordered by
// `ICustomFormatSpecification.Order` ascending) with the
// ReleaseTitleSpecification carrying its 6 hard-coded regex
// presets.
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
