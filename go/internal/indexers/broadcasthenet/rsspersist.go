package broadcasthenet

import (
	"encoding/json"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// ParseLastRecentTorrentID extracts a BroadcastheNet torrent id from
// the persisted LastRssSyncReleaseInfo embedded JSON. Mirrors C#
// `BroadcastheNet.cs:31` —
// `int.TryParse(releaseInfo.Guid.Replace("BTN-", string.Empty), out
// var torrentId)`. Returns 0 (the [Indexer.SetLastRecentTorrentID]
// "no watermark" sentinel) for every failure mode the C# call could
// silently swallow:
//
//   - Empty input (no persisted snapshot yet — the bootstrap case).
//   - JSON that doesn't unmarshal.
//   - JSON missing the `guid` field, or with an empty guid string.
//   - Guid that doesn't start with the literal `BTN-` prefix (a
//     foreign indexer's row, e.g. `nzbgeek-7` — won't happen in
//     practice because each providerId is single-implementation, but
//     defensive parity with the C# `int.TryParse` swallow keeps
//     callers from having to know the difference).
//   - Guid where the post-`BTN-` payload isn't parseable as an int.
//
// The 0-return convention deliberately matches the C# behaviour:
// `BroadcastheNet.cs:25-37` declares a local `torrentId` only inside
// the `if` arm, and only seeds `requestGenerator.LastRecentTorrentId`
// when both the row and the parse succeed — every other path leaves
// the request-generator's default (0) untouched, which is exactly
// what the Go-port [Indexer.SetLastRecentTorrentID] treats as
// "watermark unset, walk the age-only tier" per
// [Indexer.FetchRecent].
//
// Slice 5.J177 (audit B.6 BTN persistence-side, slice 2 of 3). The
// J178 wiring slice will call this on indexer-adapter construction
// to seed [Indexer.SetLastRecentTorrentID]; J176 added the
// [indexerstatus.Service.GetLastRssSyncReleaseInfo] read-path this
// consumes.
func ParseLastRecentTorrentID(releaseInfoJSON string) int {
	if releaseInfoJSON == "" {
		return 0
	}
	var probe struct {
		Guid string `json:"guid"`
	}
	if err := json.Unmarshal([]byte(releaseInfoJSON), &probe); err != nil {
		return 0
	}
	const prefix = "BTN-"
	if !strings.HasPrefix(probe.Guid, prefix) {
		return 0
	}
	id, err := strconv.Atoi(probe.Guid[len(prefix):])
	if err != nil {
		return 0
	}
	return id
}

// EncodeLastRecentRelease picks the newest release from a FetchRecent
// result slice and marshals its embedded [parser.ReleaseInfo] as a
// JSON document suitable for
// [indexerstatus.Service.UpdateRssSyncStatus]. Mirrors the C# write
// path at `HttpIndexerBase.cs:209-219`:
//
//	var ordered = releases.OrderByDescending(v => v.PublishDate).ToList();
//	...
//	lastReleaseInfo = ordered.First();
//	_indexerStatusService.UpdateRssSyncStatus(Definition.Id, lastReleaseInfo);
//
// The Go port intentionally marshals the embedded
// [parser.ReleaseInfo] value (not the full [parser.TorrentInfo])
// because:
//
//  1. The C# column is typed `ReleaseInfo` (`IndexerStatus.cs:8`), so
//     Newtonsoft serialises only the declared-type fields — torrent
//     extras (InfoHash, MagnetURL, Seeders, Peers) are absent from
//     the persisted shape. Matching this keeps the persisted JSON
//     compact and parity-aligned.
//  2. The downstream read path (this file's
//     [ParseLastRecentTorrentID] and the existing C# parse at
//     `BroadcastheNet.cs:31`) only reads the `guid` field. Including
//     torrent extras would bloat the column with bytes nobody
//     reads.
//
// Tie-breaking on PublishDate equality matches `OrderByDescending`'s
// LINQ-stable behaviour — Go's sort.SliceStable is used (the C# LINQ
// `OrderByDescending` is documented stable on .NET). Empty / nil
// input slices return `""`, which the caller (J178 wiring) uses to
// skip the [indexerstatus.Service.UpdateRssSyncStatus] call entirely
// (matches the C# `if (isRecent && !releases.Empty())` guard at
// `HttpIndexerBase.cs:207`).
//
// JSON marshalling errors collapse to `""` rather than propagating.
// `parser.ReleaseInfo` is a pure-data struct with no non-marshallable
// fields under the Go-port's current shape (every member is either a
// primitive, a string, a time.Time, a slice of typed values, or a
// pointer to a struct), so a marshal failure would indicate a
// programming-error mutation outside this slice's surface — the
// 0-return-on-failure convention matches the rest of this file's
// tolerance posture and keeps the J178 caller from having to handle
// a never-fires-in-practice error path.
//
// Slice 5.J177 (audit B.6 BTN persistence-side, slice 2 of 3).
func EncodeLastRecentRelease(rels []*parser.TorrentInfo) string {
	if len(rels) == 0 {
		return ""
	}
	newest := rels[0]
	for _, r := range rels[1:] {
		if r == nil {
			continue
		}
		if newest == nil || r.PublishDate.After(newest.PublishDate) {
			newest = r
		}
	}
	if newest == nil {
		return ""
	}
	payload, err := json.Marshal(newest.ReleaseInfo)
	if err != nil {
		return ""
	}
	return string(payload)
}
