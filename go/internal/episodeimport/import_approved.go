package episodeimport

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/history"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/quality"
)

// --- sentinel errors ---------------------------------------------

// ErrRootFolderNotFound mirrors the C# `RootFolderNotFoundException`
// caught at `ImportApprovedEpisodes.cs:174-180`. The orchestrator
// translates it into an ImportResult with the
// "Failed to import episode, Root folder missing." message + a
// fired EpisodeImportFailedEvent.
//
// The deeper writer (`episodefilemoving`) returns its own
// `ErrRootFolderNotFound` sentinel; this one is intentionally
// separate so package consumers can errors.Is against THIS sentinel
// when they care about "the import-orchestrator failed" rather than
// the lower-level "the writer failed".
var ErrRootFolderNotFound = errors.New("episodeimport: root folder not found")

// ErrDestinationAlreadyExists mirrors the C#
// `DestinationAlreadyExistsException` caught at `:181-187`. The
// orchestrator emits an ImportResult with the
// "Destination already exists." message + queues a RescanSeriesCommand
// (so the existing file gets re-discovered and the user can decide).
var ErrDestinationAlreadyExists = errors.New("episodeimport: destination already exists")

// ErrRecycleBin mirrors `RecycleBinException` caught at `:188-194`.
// Triggered when the upgrade flow can't move an existing file to
// the recycle bin.
var ErrRecycleBin = errors.New("episodeimport: recycle bin failure")

// --- narrow interfaces -------------------------------------------

// MediaFileWriter is the slice of [episodefiles.Service] the
// orchestrator reaches for. Three methods —
//
//   - Add(file) → returns the persisted file with ID populated.
//   - GetFilesWithRelativePath(seriesID, relativePath) → for the
//     non-newDownload path's "delete previous files mapping to
//     this path" pre-pass at C# `:142-148`.
//   - Delete(file, reason) → for the same pre-pass.
type MediaFileWriter interface {
	Add(ctx context.Context, file *episodefiles.File) (*episodefiles.File, error)
	GetFilesWithRelativePath(ctx context.Context, seriesID int, relativePath string) ([]episodefiles.File, error)
	Delete(ctx context.Context, file *episodefiles.File, reason episodefiles.DeleteMediaFileReason) error
}

// MediaFileUpgrader is the slice of `IUpgradeMediaFiles` the
// orchestrator reaches for at C# `:138`. The full C# class handles
// the recycle-bin / replace-existing flow; the Go port narrows to
// the single method the orchestrator calls.
//
// nil-tolerant via [noopMediaFileUpgrader]: the default treats every
// upgrade as "no existing file to recycle" — appropriate when the
// upgrader subsystem ports later. Production wiring binds a real
// implementation that handles the move-to-recycle-bin + EpisodeFile
// row replacement.
type MediaFileUpgrader interface {
	// UpgradeEpisodeFile transfers the file into place (calling the
	// MoveEpisodeFile/CopyEpisodeFile writer under the hood) and
	// recycles any pre-existing files with the same RelativePath.
	// Returns the list of recycled files (`OldFiles` in the C# tree)
	// + any error.
	UpgradeEpisodeFile(ctx context.Context, file *episodefiles.File, le *LocalEpisode, copyOnly bool) (oldFiles []episodefiles.DeletedFile, err error)
}

// ExtrasImporter is the slice of `IExtraService` + `IExistingExtraFiles`
// the orchestrator reaches for at C# `:158-169`. Three methods,
// nil-tolerant via [noopExtrasImporter]:
//
//   - ImportEpisode runs the script-import → extras-detection flow
//     when ScriptImported=false OR ShouldImportExtras=true.
//   - MoveFilesAfterRename moves companion extras files when the
//     script-imported file's relative path differs from the writer's.
//   - ImportExtraFiles runs the extras-detection flow on the script-
//     import result.
//
// All three default to no-op until the extras subsystem ports.
type ExtrasImporter interface {
	ImportEpisode(le *LocalEpisode, file *episodefiles.File, copyOnly bool) error
	MoveFilesAfterRename(le *LocalEpisode, file *episodefiles.File) error
	ImportExtraFiles(le *LocalEpisode, possibleExtraFiles []string, oldRelativePath string) error
}

// HistoryGrabFinderForImport narrows the C# `_historyService.FindByDownloadId`
// + LINQ filter+orderby chain at `:92-94` into one method that
// returns the single most-recent Grabbed history.Entry (or nil).
//
// Reuses the same shape as [HistoryGrabsFinder] (slice 3.61) but
// returns the post-filter most-recent entry directly so the
// orchestrator stays compact. nil result with nil error means
// "no grabbed event for this DownloadID".
//
// nil-tolerant via [noopHistoryGrabFinder]: the default returns
// (nil, nil) so the orchestrator's IndexerFlags / ReleaseType
// enrichment branches at `:90-108` skip cleanly when the history
// subsystem isn't wired.
type HistoryGrabFinderForImport interface {
	MostRecentGrabbed(ctx context.Context, downloadID string) (*history.Entry, error)
}

// QualityComparer narrows the C# `QualityModelComparer.Compare`
// at `:63`. Returns a 3-way comparison between two Quality models
// in the supplied series's QualityProfile context (negative iff a<b,
// 0 iff equal, positive iff a>b).
//
// Production wiring binds a [decisionengine.CompareQualityModels]-
// based adapter that looks up the series's QualityProfile.
//
// nil-tolerant via [noopQualityComparer]: defaults to "all qualities
// equal" — preserves stable ordering by caller-controlled fields
// (e.g. Size) when no QualityProfile is wired.
type QualityComparer interface {
	Compare(seriesID int, left, right quality.Model) int
}

// ImportEventPublisher narrows `IEventAggregator.PublishEvent` for
// the orchestrator-emitted [EpisodeImportedEvent] +
// [EpisodeImportFailedEvent]. nil-tolerant via [noopImportEventPublisher].
type ImportEventPublisher interface {
	Publish(event any)
}

// RescanSeriesCommander narrows `IManageCommandQueue.Push(RescanSeriesCommand)`
// at C# `:186`. Triggers a re-scan of the series's library folder so
// the existing-file conflict gets re-discovered. nil-tolerant via
// [noopRescanCommander].
type RescanSeriesCommander interface {
	PushRescanSeries(seriesID int)
}

// --- defaults ------------------------------------------------------

type noopMediaFileUpgrader struct{}

// UpgradeEpisodeFile returns nil OldFiles + nil err — appropriate for
// the no-existing-file case.
func (noopMediaFileUpgrader) UpgradeEpisodeFile(context.Context, *episodefiles.File, *LocalEpisode, bool) ([]episodefiles.DeletedFile, error) {
	return nil, nil
}

type noopExtrasImporter struct{}

// ImportEpisode is a no-op.
func (noopExtrasImporter) ImportEpisode(*LocalEpisode, *episodefiles.File, bool) error { return nil }

// MoveFilesAfterRename is a no-op.
func (noopExtrasImporter) MoveFilesAfterRename(*LocalEpisode, *episodefiles.File) error { return nil }

// ImportExtraFiles is a no-op.
func (noopExtrasImporter) ImportExtraFiles(*LocalEpisode, []string, string) error { return nil }

type noopHistoryGrabFinder struct{}

// MostRecentGrabbed returns (nil, nil) — no enrichment.
func (noopHistoryGrabFinder) MostRecentGrabbed(context.Context, string) (*history.Entry, error) {
	return nil, nil
}

type noopQualityComparer struct{}

// Compare always returns 0 — caller sort falls back to secondary
// keys (Size).
func (noopQualityComparer) Compare(int, quality.Model, quality.Model) int { return 0 }

type noopImportEventPublisher struct{}

// Publish drops the event.
func (noopImportEventPublisher) Publish(any) {}

type noopRescanCommander struct{}

// PushRescanSeries drops the command.
func (noopRescanCommander) PushRescanSeries(int) {}

// --- events --------------------------------------------------------

// EpisodeImportedEvent mirrors the C#
// `MediaFiles/Events/EpisodeImportedEvent.cs` payload. Emitted after
// a successful Add via [ImportEventPublisher.Publish].
type EpisodeImportedEvent struct {
	LocalEpisode       *LocalEpisode
	EpisodeFile        *episodefiles.File
	OldFiles           []episodefiles.DeletedFile
	NewDownload        bool
	DownloadClientItem *downloadclients.Item
}

// EpisodeImportFailedEvent mirrors `EpisodeImportFailedEvent.cs`.
// Emitted on RootFolderNotFound / RecycleBin failure paths.
type EpisodeImportFailedEvent struct {
	Err                error
	LocalEpisode       *LocalEpisode
	NewDownload        bool
	DownloadClientItem *downloadclients.Item
}

// --- ImportApprovedEpisodes --------------------------------------

// ImportApprovedEpisodes ports
// `MediaFiles/EpisodeImport/ImportApprovedEpisodes.cs:24-208` — the
// orchestrator that takes a list of approved [ImportDecision]s + an
// optional download-client item and walks each decision through
// the upgrade-and-persist flow.
//
// Goroutine-safe: stateless beyond the wired deps.
type ImportApprovedEpisodes struct {
	upgrader MediaFileUpgrader
	files    MediaFileWriter
	extras   ExtrasImporter
	disk     FileSizeReader
	history  HistoryGrabFinderForImport
	quality  QualityComparer
	events   ImportEventPublisher
	rescan   RescanSeriesCommander
	log      *slog.Logger
}

// NewImportApprovedEpisodes binds the dependencies. files / disk are
// REQUIRED. The remaining deps are OPTIONAL (nil-tolerant) — see
// the per-interface doc for the no-op default's behaviour.
//
// Mirrors the slice-3.55+ constructor pattern: required-nil errors,
// optional-nil defaults to a no-op binding.
func NewImportApprovedEpisodes(
	files MediaFileWriter,
	disk FileSizeReader,
	upgrader MediaFileUpgrader,
	extras ExtrasImporter,
	historyFinder HistoryGrabFinderForImport,
	qcmp QualityComparer,
	events ImportEventPublisher,
	rescan RescanSeriesCommander,
	log *slog.Logger,
) (*ImportApprovedEpisodes, error) {
	if files == nil {
		return nil, errors.New("episodeimport: NewImportApprovedEpisodes: nil MediaFileWriter")
	}
	if disk == nil {
		return nil, errors.New("episodeimport: NewImportApprovedEpisodes: nil FileSizeReader")
	}
	if upgrader == nil {
		upgrader = noopMediaFileUpgrader{}
	}
	if extras == nil {
		extras = noopExtrasImporter{}
	}
	if historyFinder == nil {
		historyFinder = noopHistoryGrabFinder{}
	}
	if qcmp == nil {
		qcmp = noopQualityComparer{}
	}
	if events == nil {
		events = noopImportEventPublisher{}
	}
	if rescan == nil {
		rescan = noopRescanCommander{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &ImportApprovedEpisodes{
		files:    files,
		disk:     disk,
		upgrader: upgrader,
		extras:   extras,
		history:  historyFinder,
		quality:  qcmp,
		events:   events,
		rescan:   rescan,
		log:      log,
	}, nil
}

// Import ports the public `Import` method at C# `:57-207`. Walks the
// decisions, sorts approved ones (per-series quality DESC, size DESC),
// processes each via the upgrade-and-add flow, and appends rejected
// decisions at the end as ImportResults with rejection messages.
//
// downloadClientItem may be nil for manual-import / library-rescan
// flows. importMode controls the move/copy/auto decision.
func (s *ImportApprovedEpisodes) Import(ctx context.Context, decisions []*ImportDecision, newDownload bool, item *downloadclients.Item, mode ImportMode) ([]*ImportResult, error) {
	if s == nil {
		return nil, errors.New("episodeimport: ImportApprovedEpisodes.Import on nil receiver")
	}

	// Filter approved decisions, group + sort.
	approved := s.sortApproved(decisions)

	// Process each in order; later ImportResults can short-circuit
	// duplicates via the AlreadyImported check at C# `:78-85`.
	results := make([]*ImportResult, 0, len(approved))
	for _, d := range approved {
		results = append(results, s.processOne(ctx, d, results, newDownload, item, mode))
	}

	// C# `:202-204`: append rejected decisions as Rejected ImportResults.
	for _, d := range decisions {
		if d == nil || d.Approved() {
			continue
		}
		errs := make([]string, 0, len(d.Rejections))
		for _, r := range d.Rejections {
			errs = append(errs, r.Message)
		}
		results = append(results, NewSkippedImportResult(d, errs...))
	}

	return results, nil
}

// sortApproved ports the C# LINQ chain at `:59-70`. Filter approved
// → group by SeriesID → within group sort by Quality DESC then Size
// DESC → flatten → re-sort by min(EpisodeNumber) ASC then Size DESC.
func (s *ImportApprovedEpisodes) sortApproved(decisions []*ImportDecision) []*ImportDecision {
	approved := make([]*ImportDecision, 0, len(decisions))
	for _, d := range decisions {
		if d != nil && d.Approved() && d.LocalEpisode != nil && d.LocalEpisode.Series != nil {
			approved = append(approved, d)
		}
	}

	// Per-series Quality DESC + Size DESC.
	slices.SortStableFunc(approved, func(a, b *ImportDecision) int {
		// Series ID first (stable group).
		if a.LocalEpisode.Series.ID != b.LocalEpisode.Series.ID {
			return a.LocalEpisode.Series.ID - b.LocalEpisode.Series.ID
		}
		// Within the same series: Quality DESC.
		if cmp := s.quality.Compare(a.LocalEpisode.Series.ID, a.LocalEpisode.Quality, b.LocalEpisode.Quality); cmp != 0 {
			return -cmp // DESC
		}
		// Then Size DESC.
		switch {
		case a.LocalEpisode.Size > b.LocalEpisode.Size:
			return -1
		case a.LocalEpisode.Size < b.LocalEpisode.Size:
			return 1
		}
		return 0
	})

	// C# `:69-70`: re-sort by min(EpisodeNumber) ASC then Size DESC.
	// Stable sort preserves the per-series ordering for ties.
	slices.SortStableFunc(approved, func(a, b *ImportDecision) int {
		ea := minEpisodeNumber(a)
		eb := minEpisodeNumber(b)
		if ea != eb {
			return ea - eb
		}
		switch {
		case a.LocalEpisode.Size > b.LocalEpisode.Size:
			return -1
		case a.LocalEpisode.Size < b.LocalEpisode.Size:
			return 1
		}
		return 0
	})

	return approved
}

// minEpisodeNumber returns the minimum EpisodeNumber across the
// decision's matched episodes, or 0 when empty (matches C#
// `MinOrDefault()` returning the type default).
func minEpisodeNumber(d *ImportDecision) int {
	if d == nil || d.LocalEpisode == nil {
		return 0
	}
	if len(d.LocalEpisode.Episodes) == 0 {
		return 0
	}
	min := d.LocalEpisode.Episodes[0].EpisodeNumber
	for _, e := range d.LocalEpisode.Episodes[1:] {
		if e.EpisodeNumber < min {
			min = e.EpisodeNumber
		}
	}
	return min
}

// processOne runs the per-decision import body. Returns the
// ImportResult to append to the list.
func (s *ImportApprovedEpisodes) processOne(
	ctx context.Context,
	d *ImportDecision,
	priorResults []*ImportResult,
	newDownload bool,
	item *downloadclients.Item,
	mode ImportMode,
) *ImportResult {
	le := d.LocalEpisode

	// C# `:78-85`: skip if any of this decision's episodes already
	// imported in a prior result of THIS run.
	if alreadyImportedInRun(priorResults, le) {
		return NewSkippedImportResult(d, "Episode has already been imported")
	}

	defer func() {
		// Untyped recovery — match C# `catch (Exception)` at `:195-199`.
		if r := recover(); r != nil {
			s.log.Warn("episodeimport: panic during import",
				"path", le.Path, "panic", r)
		}
	}()

	file := le.ToEpisodeFile()
	if file == nil {
		return NewSkippedImportResult(d, "Failed to import episode")
	}

	// C# `:88`: populate Size after the ToEpisodeFile constructor.
	if size, err := s.disk.GetFileSize(le.Path); err == nil {
		file.Size = size
	} else {
		s.log.Warn("episodeimport: GetFileSize failed", "path", le.Path, "err", err)
	}

	// C# `:90-108`: enrich IndexerFlags + ReleaseType from history.
	// Skipped under the noop default; production binding parses
	// the history.Entry.Data map for the "indexerFlags" + "releaseType"
	// keys (both stored as Enum.ToString() forms by the
	// EpisodeGrabbedEvent handler).
	if item != nil && item.DownloadID != "" {
		if grab, err := s.history.MostRecentGrabbed(ctx, item.DownloadID); err == nil && grab != nil {
			// The Data map's "indexerFlags" / "releaseType" parsing
			// is intentionally minimal here — the C# Enum.TryParse
			// accepts both numeric-int and Enum-name forms; the Go
			// port handles only the numeric-int form to keep this
			// slice focused. A future slice can layer name-form
			// parsing on top without changing the call site.
			if flagsStr, ok := grab.Data["indexerFlags"]; ok {
				if flags := parseIntStr(flagsStr); flags != 0 {
					file.IndexerFlags = flags
				}
			}
			if rtStr, ok := grab.Data["releaseType"]; ok {
				if rt := parseIntStr(rtStr); rt != 0 {
					file.ReleaseType = rt
				}
			}
		}
	}

	// C# `:111-124`: derive copyOnly from importMode + downloadClientItem.
	copyOnly := false
	switch mode {
	case ImportModeCopy:
		copyOnly = true
	case ImportModeMove:
		copyOnly = false
	default: // Auto
		copyOnly = item != nil && !item.CanMoveFiles
	}

	var oldFiles []episodefiles.DeletedFile
	if newDownload {
		// C# `:128-136`: when the download item has an OutputPath
		// that's a parent of the LocalEpisode.Path, override
		// OriginalFilePath with the path relative to the
		// OutputPath's parent directory. The Go-port `OutputPath` is
		// `string` rather than the C# `OsPath`; the parent-walk
		// logic stays equivalent.
		if item != nil && !item.OutputPath.IsEmpty() {
			// outputDirectory is the parent of OutputPath. The C#
			// `_.Directory.ToString()` returns the OutputPath's
			// containing folder.
			outputDir := dirOf(item.OutputPath.String())
			if outputDir != "" && pathIsParent(outputDir, le.Path) {
				if rel := relativePathOrEmpty(outputDir, le.Path); rel != "" {
					file.OriginalFilePath = rel
				}
			}
		}

		ofs, err := s.upgrader.UpgradeEpisodeFile(ctx, file, le, copyOnly)
		if err != nil {
			return s.handleUpgradeError(d, le, err, newDownload, item)
		}
		oldFiles = ofs
	} else {
		// C# `:142-148`: delete pre-existing files mapping to this
		// path (the rescan / library-add flow).
		previous, err := s.files.GetFilesWithRelativePath(ctx, le.Series.ID, file.RelativePath)
		if err != nil {
			s.log.Warn("episodeimport: GetFilesWithRelativePath failed",
				"path", le.Path, "err", err)
		}
		for i := range previous {
			if err := s.files.Delete(ctx, &previous[i], episodefiles.ReasonManualOverride); err != nil {
				s.log.Warn("episodeimport: Delete previous file failed",
					"id", previous[i].ID, "err", err)
			}
		}
	}

	persisted, err := s.files.Add(ctx, file)
	if err != nil {
		s.log.Warn("episodeimport: Add failed", "path", le.Path, "err", err)
		return NewSkippedImportResult(d, "Failed to import episode")
	}

	// C# `:154-170`: extras.
	if newDownload {
		if le.ScriptImported {
			if err := s.extras.ImportExtraFiles(le, le.PossibleExtraFiles, le.FileNameBeforeRename); err != nil {
				s.log.Warn("episodeimport: ImportExtraFiles failed", "err", err)
			}
			if le.FileNameBeforeRename != persisted.RelativePath {
				if err := s.extras.MoveFilesAfterRename(le, persisted); err != nil {
					s.log.Warn("episodeimport: MoveFilesAfterRename failed", "err", err)
				}
			}
		}
		if !le.ScriptImported || le.ShouldImportExtras {
			if err := s.extras.ImportEpisode(le, persisted, copyOnly); err != nil {
				s.log.Warn("episodeimport: ImportEpisode (extras) failed", "err", err)
			}
		}
	}

	// C# `:172`: emit the EpisodeImportedEvent.
	s.events.Publish(EpisodeImportedEvent{
		LocalEpisode:       le,
		EpisodeFile:        persisted,
		OldFiles:           oldFiles,
		NewDownload:        newDownload,
		DownloadClientItem: item,
	})

	return NewImportedImportResult(d, persisted)
}

// handleUpgradeError ports the C# `catch` arms at `:174-199`.
// Maps the supplied error to one of the documented sentinels and
// constructs the matching ImportResult.
func (s *ImportApprovedEpisodes) handleUpgradeError(d *ImportDecision, le *LocalEpisode, err error, newDownload bool, item *downloadclients.Item) *ImportResult {
	// le is non-nil at every call site (guarded above), but the
	// defensive sentinel keeps the linter quiet about the
	// le.Series.ID deref in the DestinationAlreadyExists arm.
	var lePath string
	if le != nil {
		lePath = le.Path
	}
	switch {
	case errors.Is(err, ErrRootFolderNotFound):
		s.log.Warn("episodeimport: root folder missing", "path", lePath, "err", err)
		s.events.Publish(EpisodeImportFailedEvent{Err: err, LocalEpisode: le, NewDownload: newDownload, DownloadClientItem: item})
		return NewSkippedImportResult(d, "Failed to import episode, Root folder missing.")
	case errors.Is(err, ErrDestinationAlreadyExists):
		s.log.Warn("episodeimport: destination already exists", "path", lePath, "err", err)
		if le != nil && le.Series != nil {
			s.rescan.PushRescanSeries(le.Series.ID)
		}
		return NewSkippedImportResult(d, "Failed to import episode, Destination already exists.")
	case errors.Is(err, ErrRecycleBin):
		s.log.Warn("episodeimport: recycle bin failure", "path", lePath, "err", err)
		s.events.Publish(EpisodeImportFailedEvent{Err: err, LocalEpisode: le, NewDownload: newDownload, DownloadClientItem: item})
		return NewSkippedImportResult(d, "Failed to import episode, unable to move existing file to the Recycle Bin.")
	default:
		s.log.Warn("episodeimport: import failed", "path", lePath, "err", err)
		return NewSkippedImportResult(d, "Failed to import episode")
	}
}

// alreadyImportedInRun mirrors the C# AlreadyImported check at
// `:78-85`: returns true iff any prior result's LocalEpisode
// shares an episode ID with the supplied LocalEpisode.
func alreadyImportedInRun(prior []*ImportResult, le *LocalEpisode) bool {
	if le == nil || len(le.Episodes) == 0 {
		return false
	}
	current := make(map[int]struct{}, len(le.Episodes))
	for _, e := range le.Episodes {
		current[e.ID] = struct{}{}
	}
	for _, r := range prior {
		if r == nil || r.Decision == nil || r.Decision.LocalEpisode == nil {
			continue
		}
		for _, e := range r.Decision.LocalEpisode.Episodes {
			if _, ok := current[e.ID]; ok {
				return true
			}
		}
	}
	return false
}

// parseIntStr does a permissive integer parse of a string. Returns
// 0 on any failure — matches the C# `Enum.TryParse` failure path
// where the out var is the enum's zero value.
func parseIntStr(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	negative := false
	for i, c := range s {
		if i == 0 && c == '-' {
			negative = true
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if negative {
		return -n
	}
	return n
}

// dirOf returns the parent directory of the supplied path. Wraps
// `filepath.Dir` with a defensive guard for empty input.
func dirOf(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

// pathIsParent reports whether parent is an ancestor directory of
// child. Wraps the existing [ospath.IsParent] for use inside this
// package without bringing the import to the public surface.
func pathIsParent(parent, child string) bool {
	return ospath.IsParent(parent, child)
}

// relativePathOrEmpty returns the path relative to base, or the
// empty string on error. Used by the OriginalFilePath override
// branch.
func relativePathOrEmpty(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return ""
	}
	return rel
}

// Compile-time interface-satisfaction guards. Refactor breakage at
// build time rather than wiring time.
var (
	_ MediaFileUpgrader          = noopMediaFileUpgrader{}
	_ ExtrasImporter             = noopExtrasImporter{}
	_ HistoryGrabFinderForImport = noopHistoryGrabFinder{}
	_ QualityComparer            = noopQualityComparer{}
	_ ImportEventPublisher       = noopImportEventPublisher{}
	_ RescanSeriesCommander      = noopRescanCommander{}
)
