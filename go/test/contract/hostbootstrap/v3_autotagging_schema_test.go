package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3AutoTaggingSchemaContract pins /api/v3/autotagging/schema
// to the C#-recorded golden — 11 specification schemas (sorted by
// `IAutoTaggingSpecification.Order` ASC then Implementation ASC).
// No presets, no infoLink (the AutoTagSpecificationBase has no
// InfoLink property, unlike CustomFormatSpecificationBase).
func TestV3AutoTaggingSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.autotagging.schema"
	checkEmptyJSONContract(t, entryID, apiv3.AutoTaggingSchemaHandler())
}
