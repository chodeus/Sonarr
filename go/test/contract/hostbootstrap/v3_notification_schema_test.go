package hostbootstrap

import (
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3NotificationSchemaContract pins /api/v3/notification/schema
// to the C#-recorded golden. 25 providers sorted by ImplementationName
// (case-insensitive, so ntfy.sh sits between Notifiarr and Plex Media
// Server). Wire shape is rich with 13 on* / 13 supportsOn* booleans
// + includeHealthWarnings + the optional message banner (CustomScript
// is the sole carrier in current Sonarr).
func TestV3NotificationSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.notification.schema"
	checkEmptyJSONContract(t, entryID, apiv3.NotificationSchemaHandler())
}
