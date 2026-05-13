package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3MetadataSchemaContract pins /api/v3/metadata/schema to the
// C#-recorded golden — 4 metadata providers (Xbmc, Plex, Roksbox,
// Wdtv) in C# DI assembly-scan order. Each carries the per-provider
// checkbox-fields list with section + advanced flags + default
// values matching the IProviderConfig constructor.
func TestV3MetadataSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.metadata.schema"
	checkEmptyJSONContract(t, entryID, apiv3.MetadataSchemaHandler())
}
