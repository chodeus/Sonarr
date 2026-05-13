package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
)

// DelayProfileResource mirrors
// `src/Sonarr.Api.V3/Profiles/Delay/DelayProfileResource.cs`.
//
// C# field shapes that need explicit handling:
//   - `preferredProtocol` is the DownloadProtocol enum, rendered by
//     JsonStringEnumConverter+CamelCase as the lower-case enum name
//     ("usenet" / "torrent" / "unknown"). The Go Profile stores the
//     int form; we resolve at the API edge via DownloadProtocolName.
//   - `minimumCustomFormatScore` is a non-nullable int in C# and is
//     always emitted as a number. The Go Profile carries *int (the
//     DB column is nullable); the mapper unwraps nil → 0 so the wire
//     shape always emits a number.
//   - `tags` is a HashSet<int> rendered as `[]` when empty. The
//     mapper nil-normalises to `[]int{}`.
type DelayProfileResource struct {
	ID                             int    `json:"id"`
	EnableUsenet                   bool   `json:"enableUsenet"`
	EnableTorrent                  bool   `json:"enableTorrent"`
	PreferredProtocol              string `json:"preferredProtocol"`
	UsenetDelay                    int    `json:"usenetDelay"`
	TorrentDelay                   int    `json:"torrentDelay"`
	BypassIfHighestQuality         bool   `json:"bypassIfHighestQuality"`
	BypassIfAboveCustomFormatScore bool   `json:"bypassIfAboveCustomFormatScore"`
	MinimumCustomFormatScore       int    `json:"minimumCustomFormatScore"`
	Order                          int    `json:"order"`
	Tags                           []int  `json:"tags"`
}

// DownloadProtocolName mirrors C# `JsonStringEnumConverter(JsonNamingPolicy
// .CamelCase)` applied to `DownloadProtocol` at
// `src/NzbDrone.Core/Indexers/DownloadProtocol.cs:3-9`. Three enum
// values map to lowercase names; anything outside that range falls
// back to "unknown" matching the C# zero-value.
//
// Exported so future indexer / queue / blocklist controllers reuse
// the mapping without duplicating the table.
func DownloadProtocolName(p indexers.DownloadProtocol) string {
	switch p {
	case indexers.DownloadProtocolUsenet:
		return "usenet"
	case indexers.DownloadProtocolTorrent:
		return "torrent"
	}
	return "unknown"
}

// DelayProfileLister is the read side the handler depends on.
// *delayprofiles.Service satisfies it via All.
type DelayProfileLister interface {
	All(ctx context.Context) ([]delayprofiles.Profile, error)
}

// DelayProfileHandler returns the http.Handler for
// GET /api/v3/delayprofile. Mirrors
// `DelayProfileController.GetAll` at
// `src/Sonarr.Api.V3/Profiles/Delay/DelayProfileController.cs:71-76`.
//
// Service.All preserves repo Order-by-Id ordering — the default
// global profile (Id=1, Order=INT_MAX) is the only row on a fresh
// DB.
func DelayProfileHandler(lister DelayProfileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("delayprofiles: %v", err), http.StatusInternalServerError)
			return
		}
		out := make([]DelayProfileResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toDelayProfileResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toDelayProfileResource(p delayprofiles.Profile) DelayProfileResource {
	tags := p.Tags
	if tags == nil {
		tags = []int{}
	}
	score := 0
	if p.MinimumCustomFormatScore != nil {
		score = *p.MinimumCustomFormatScore
	}
	return DelayProfileResource{
		ID:                             p.ID,
		EnableUsenet:                   p.EnableUsenet,
		EnableTorrent:                  p.EnableTorrent,
		PreferredProtocol:              DownloadProtocolName(indexers.DownloadProtocol(p.PreferredProtocol)),
		UsenetDelay:                    p.UsenetDelay,
		TorrentDelay:                   p.TorrentDelay,
		BypassIfHighestQuality:         p.BypassIfHighestQuality,
		BypassIfAboveCustomFormatScore: p.BypassIfAboveCustomFormatScore,
		MinimumCustomFormatScore:       score,
		Order:                          p.Order,
		Tags:                           tags,
	}
}
