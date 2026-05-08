// Boot-time adapters that bridge the real *episodeimport types into
// the narrow [mediafiles.ImportDecisionMaker] /
// [mediafiles.ImportApprovedEpisodes] interfaces consumed by
// *mediafiles.Service.Scan. Same posture as
// downloadedimport_wiring.go: an adapter where a real type exists
// but its surface differs from what mediafiles expects (signature
// shape, []any vs []*ImportDecision, options-struct vs bool, extra
// args).
//
// Until slice 3.AE the host wired stub implementations
// (mediafiles_stubs.go) which short-circuited every Scan to "no
// decisions" / "no-op import"; the real adapters here mean
// RescanSeries actually walks the slice-3.95 ImportDecisionMaker's
// 11 specs against unmapped files and commits the approved subset
// via the slice-3.98 ImportApprovedEpisodes.

package main

import (
	"context"
	"fmt"

	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/series"
)

// mediafilesImportDecisionAdapter wraps the real
// [*episodeimport.ImportDecisionMaker] to satisfy
// [mediafiles.ImportDecisionMaker]. Two shape mismatches:
//
//  1. Options struct vs bare bool — narrow interface takes
//     `filterExistingFiles bool`; real takes
//     [episodeimport.GetImportDecisionsOptions]. Adapter fills in
//     just FilterExistingFiles; every other option (DownloadItem,
//     DownloadItemInfo, FolderInfo, SceneSource) defaults to its
//     zero value, matching the library-rescan code path C#
//     `DiskScanService.cs:79` which passes only the four-arg
//     `MakeImportDecision` overload.
//
//  2. Return type — narrow interface returns `[]any`; real returns
//     `[]*ImportDecision`. The adapter widens each pointer into
//     `any`. The ImportApproved adapter narrows it back via type
//     assertion at commit time.
type mediafilesImportDecisionAdapter struct {
	real *episodeimport.ImportDecisionMaker
}

func (a mediafilesImportDecisionAdapter) GetImportDecisions(
	ctx context.Context,
	files []string,
	parent *series.Series,
	filterExistingFiles bool,
) ([]any, error) {
	decisions, err := a.real.GetImportDecisions(ctx, files, parent,
		episodeimport.GetImportDecisionsOptions{FilterExistingFiles: filterExistingFiles})
	if err != nil {
		return nil, err
	}
	out := make([]any, len(decisions))
	for i, d := range decisions {
		out[i] = d
	}
	return out, nil
}

// mediafilesImportApprovedAdapter wraps the real
// [*episodeimport.ImportApprovedEpisodes] to satisfy
// [mediafiles.ImportApprovedEpisodes].
//
// Three shape mismatches:
//
//  1. Decisions type — narrow interface carries `[]any`; real
//     takes `[]*ImportDecision`. Adapter narrows via type assertion;
//     a wrong type surfaces a typed error rather than panicking
//     because mediafiles.Service.Scan already swallows / logs the
//     return value.
//
//  2. Bool naming — narrow interface names the bool `copyOnly`,
//     but the real signature names it `newDownload`. The C# tree's
//     `IImportApprovedEpisodes.Import(decisions, newDownload, …)`
//     matches the real Go signature; the narrow-interface name in
//     `internal/mediafiles/deps.go` is a misnomer. The single
//     caller (mediafiles.Service.Scan at `service.go:225`) always
//     passes `false`, so the misnomer is benign — the adapter
//     forwards the bool verbatim into newDownload, preserving the
//     C# `DiskScanService` semantic of "library rescan, not a
//     fresh download".
//
//  3. Extra args — real signature also takes
//     `*downloadclients.Item` + [episodeimport.ImportMode]. For
//     the rescan path the item is nil and the mode defaults to
//     ImportModeAuto (no override; with item==nil and Auto the
//     real Import resolves copyOnly=false → move, matching the
//     C# rescan-path behaviour at
//     `ImportApprovedEpisodes.cs:111-124`).
//
// Result list is dropped — mediafiles.Service.Scan doesn't read it
// (matches C# `DiskScanService.cs:143`'s `_importApprovedEpisodes
// .Import(...)` call which discards the return).
type mediafilesImportApprovedAdapter struct {
	real *episodeimport.ImportApprovedEpisodes
}

func (a mediafilesImportApprovedAdapter) Import(ctx context.Context, decisions []any, copyOnly bool) error {
	typed := make([]*episodeimport.ImportDecision, 0, len(decisions))
	for i, d := range decisions {
		td, ok := d.(*episodeimport.ImportDecision)
		if !ok {
			return fmt.Errorf("mediafilesImportApprovedAdapter: decision idx %d unexpected type %T", i, d)
		}
		typed = append(typed, td)
	}
	_, err := a.real.Import(ctx, typed, copyOnly /* threaded as newDownload — see doc */, nil, episodeimport.ImportModeAuto)
	return err
}

// Compile-time guards.
var (
	_ mediafiles.ImportDecisionMaker    = mediafilesImportDecisionAdapter{}
	_ mediafiles.ImportApprovedEpisodes = mediafilesImportApprovedAdapter{}
)
