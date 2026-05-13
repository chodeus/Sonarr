package hostbootstrap

import (
	"context"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
)

// TestV3UpdateContract pins /api/v3/update to the C#-recorded
// golden — an empty list on a fresh recorder (the recording host
// shipped no update packages). The RecentUpdateProvider port is
// DEFERRED until a wider Update sub-arc lands; the stub returns
// nil which serializes to `[]` and matches the recorded body.
func TestV3UpdateContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.update"
	checkEmptyJSONContract(t, entryID, apiv3.UpdateHandler(emptyUpdateLister{}))
}

type emptyUpdateLister struct{}

func (emptyUpdateLister) RecentUpdates(context.Context) ([]apiv3.UpdateResource, error) {
	return nil, nil
}
