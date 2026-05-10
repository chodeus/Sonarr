package indexersearch

import "sonarr.tv/sonarr/internal/parser"

// DeDupReleases groups the aggregated raw-release slice by Guid and
// returns one survivor per group, picking the lowest IndexerPriority
// (lower wins per IndexerDefinition.cs:14 — DefaultPriority=25, range
// 1..50, 1 is highest). Mirrors the C# DeDupeDecisions tiebreaker at
// ReleaseSearchService.cs:566-572 — minus the primary
// `Rejections.Count()` sort key, since that one keys on
// DownloadDecision and the Go port doesn't reach the decision shape
// until decisionengine wire-through (a follow-up to queue [7]).
//
// Releases inside the slice are *parser.ReleaseInfo or
// *parser.TorrentInfo (the C# upcast equivalent — see
// parser/release_info.go:117-135). Both expose Guid and IndexerPriority
// via the embedded ReleaseInfo, so the dedup keys uniformly via the
// `releaseRef` helper below.
//
// Order: the first-seen survivor wins on ties; the result preserves
// input-iteration order of the surviving entries. Mirrors the C#
// LINQ `GroupBy → OrderBy → First` shape which is order-preserving in
// the dotnet runtime.
//
// `releases` of length 0 returns nil. Entries that aren't a known
// release shape (i.e. neither *parser.ReleaseInfo nor
// *parser.TorrentInfo) are dropped — they can't carry a Guid for
// keying, and the orchestrator never produces them, so dropping is
// safer than panicking.
func DeDupReleases(releases []any) []any {
	if len(releases) == 0 {
		return nil
	}

	// Index by Guid. seenIdx records the position in `out` for the
	// current best survivor; that lets us replace in-place when a
	// higher-priority duplicate appears, preserving input order for
	// non-collision entries.
	seenIdx := make(map[string]int, len(releases))
	out := make([]any, 0, len(releases))
	for _, r := range releases {
		ref := releaseRef(r)
		if ref == nil {
			continue
		}
		if ref.Guid == "" {
			// Empty Guid: pass through without dedup. Mirrors the C#
			// GroupBy semantics — distinct empty groups all survive.
			out = append(out, r)
			continue
		}
		idx, ok := seenIdx[ref.Guid]
		if !ok {
			seenIdx[ref.Guid] = len(out)
			out = append(out, r)
			continue
		}
		// Tie-break: lower IndexerPriority wins. The default-priority
		// value DefaultPriority=25 falls out naturally since both sides
		// of the comparison see the same field; if the existing
		// survivor's priority equals the candidate's, the existing
		// (first-seen) value wins.
		existing := releaseRef(out[idx])
		if ref.IndexerPriority < existing.IndexerPriority {
			out[idx] = r
		}
	}
	return out
}

// releaseRef returns the embedded *parser.ReleaseInfo for a known
// release shape, or nil otherwise. Matches both the bare
// *parser.ReleaseInfo (from newznab) and *parser.TorrentInfo (from
// torznab/torrentrss; embeds ReleaseInfo by value, so a pointer to
// the embedded field needs the &t.ReleaseInfo upcast).
func releaseRef(r any) *parser.ReleaseInfo {
	switch v := r.(type) {
	case *parser.TorrentInfo:
		if v == nil {
			return nil
		}
		return &v.ReleaseInfo
	case *parser.ReleaseInfo:
		return v
	default:
		return nil
	}
}
