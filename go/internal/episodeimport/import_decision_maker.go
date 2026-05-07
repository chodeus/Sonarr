package episodeimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/ospath"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// MediaFileFilterer is the slice of [episodefiles.Service] the
// orchestrator reaches for at `ImportDecisionMaker.cs:71`
// (`_mediaFileService.FilterExistingFiles(...)`).
//
// Returned slice contains only the candidate paths that aren't already
// represented by an EpisodeFile row under the supplied series. Errors
// surface to the orchestrator and shortcut the slice — every file is
// then rejected with [ImportRejectionError]. (The C# tree raises
// directly out of FilterExistingFiles so the caller never sees a
// half-filtered list either.)
type MediaFileFilterer interface {
	FilterExistingFiles(ctx context.Context, candidates []string, parent *series.Series) ([]string, error)
}

// Aggregator is the slice of [AggregationService] the orchestrator
// reaches for at `ImportDecisionMaker.cs:124` (`_aggregationService.Augment`).
//
// Augment fills LocalEpisode.Episodes / Languages / Quality /
// ReleaseGroup / ReleaseHash / Release / SubtitleInfo / MediaInfo by
// walking the registered LocalEpisodeAggregator list. Returns the
// (potentially same) LocalEpisode pointer plus any error. Errors
// wrapping [ErrAugmentingFailed] map to [ImportRejectionUnableToParse];
// any other error maps to [ImportRejectionError]. Mirrors the C#
// AugmentingFailedException catch arm at `:158-160`.
type Aggregator interface {
	Augment(le *LocalEpisode, item *downloadclients.Item) (*LocalEpisode, error)
}

// SampleClassifier is the slice of [SampleDetector] the orchestrator
// reaches for at `ImportDecisionMaker.cs:216`
// (`_detectSample.IsSample(series, file, isPossibleSpecialEpisode)`).
//
// The Go DetectSample port carries two methods on [SampleDetector] —
// the LocalEpisode-driven [SampleDetector.IsSample] (called by per-spec
// SampleSpecification) and the path-driven [SampleDetector.IsSamplePath]
// the orchestrator's pre-aggregation pass needs. The interface surfaces
// the path-driven one only; the orchestrator does NOT have a constructed
// LocalEpisode yet at the GetNonSampleVideoFileCount stage so the
// LocalEpisode form would force a fake construction.
type SampleClassifier interface {
	IsSamplePath(ctx context.Context, path string, isSpecial bool, expectedRuntimeMinutes int) DetectSampleResult
}

// EpisodeInfoParser is the slice of [parser.ParsePath] the orchestrator
// reaches for at `ImportDecisionMaker.cs:115`. Pulled behind a narrow
// interface so tests can pin the parsed-info return without depending
// on the regex-driven parser engine.
//
// Production callers may pass nil to [NewImportDecisionMaker]; the
// constructor binds [defaultEpisodeInfoParser] which delegates to
// [parser.ParsePath]. Tests pass a stub.
type EpisodeInfoParser interface {
	ParsePath(path string) *parser.ParsedEpisodeInfo
}

// defaultEpisodeInfoParser binds [parser.ParsePath] behind the narrow
// interface for the production path. Stateless; safe to share.
type defaultEpisodeInfoParser struct{}

// ParsePath delegates to [parser.ParsePath].
func (defaultEpisodeInfoParser) ParsePath(path string) *parser.ParsedEpisodeInfo {
	return parser.ParsePath(path)
}

// TrackedDownloadFinder is the slice of `ITrackedDownloadService` the
// orchestrator reaches for at `ImportDecisionMaker.cs:145`
// (`_trackedDownloadService.Find(downloadId)`).
//
// The C# service hands back a `TrackedDownload` whose
// `RemoteEpisode.Release.IndexerFlags` is what the orchestrator
// actually needs. The Go port narrows the surface to that read alone:
// `FindIndexerFlags(downloadID)` returns the resolved flags + an
// `ok` flag. ok=false → orchestrator skips the field write (matching
// the C# nil chain at `:147-150`).
//
// `internal/trackeddownloads` doesn't exist yet in the Go tree.
// Production wiring fills this in once that subsystem ports; pass
// nil to [NewImportDecisionMaker] in the meantime — the orchestrator
// treats nil as "always ok=false, skip".
type TrackedDownloadFinder interface {
	FindIndexerFlags(downloadID string) (parser.IndexerFlags, bool)
}

// EpisodeCustomFormatCalculator is the slice of
// `ILocalEpisodeCustomFormatCalculationService` the orchestrator
// reaches for at `ImportDecisionMaker.cs:153`.
//
// Mutates [LocalEpisode.CustomFormats] / [LocalEpisode.CustomFormatScore]
// + the OriginalFileName-prefixed pair in place. nil-tolerant: a nil
// dep means the orchestrator skips the call entirely (no Go port of
// the service exists yet — slice 7 lands the per-LocalEpisode
// calculator alongside the writer).
//
// Named with the Episode prefix to avoid a collision with the existing
// per-spec [CustomFormatCalculator] in specs.go (which is the
// EpisodeFile-driven calculator the upgrade specs reach for). The two
// surfaces will likely converge once the LocalEpisode/EpisodeFile
// calculator subsystem ports proper.
type EpisodeCustomFormatCalculator interface {
	UpdateEpisodeCustomFormats(le *LocalEpisode)
}

// ImportDecisionMaker ports
// `MediaFiles/EpisodeImport/ImportDecisionMaker.cs:24-274` — the
// public LocalEpisode→ImportDecision pipeline. Walks every candidate
// path through:
//
//  1. parse path → FileEpisodeInfo
//  2. disk size → Size
//  3. resolve ReleaseType from DownloadClient/Folder/File chain
//  4. AggregationService.Augment fills Episodes / Quality / etc.
//  5. if Episodes empty → reject with PartialSeason / SeasonExtra /
//     InvalidSeasonOrEpisode (whichever info source flagged it)
//  6. else if download-client driven → enrich IndexerFlags from the
//     tracked-download row + run the CustomFormat calculator
//  7. evaluate every Specification, collect rejections, return
//     ImportDecision
//
// Per-file panics + errors at any stage map to either
// [ImportRejectionUnableToParse] (when the error wraps
// [ErrAugmentingFailed]) or [ImportRejectionError] (anything else),
// mirroring the C# try/catch block at `:113-167`. Per-spec panics map
// to [ImportRejectionDecisionError] mirroring C# `:201`.
//
// Goroutine-safe: the struct is read-only post-construction. Concurrent
// GetImportDecisions calls are safe as long as the wired dependencies
// are themselves goroutine-safe (the production deps are).
type ImportDecisionMaker struct {
	specs      []Specification
	filter     MediaFileFilterer
	aggregator Aggregator
	disk       FileSizeReader
	sampler    SampleClassifier
	parser     EpisodeInfoParser
	tracked    TrackedDownloadFinder
	formats    EpisodeCustomFormatCalculator
	log        *slog.Logger
}

// NewImportDecisionMaker binds the supplied dependencies into an
// orchestrator. specs / filter / aggregator / disk / sampler are
// REQUIRED; parser / tracked / formats / log are OPTIONAL (nil parser
// → [parser.ParsePath]; nil tracked → no IndexerFlags enrichment;
// nil formats → no custom-format calculation; nil log → [slog.Default]).
//
// Returns an error rather than panicking on missing required deps so
// the caller's startup wiring path can surface the misuse cleanly.
// Mirrors the constructor-validation pattern in [NewAggregationService].
func NewImportDecisionMaker(
	specs []Specification,
	filter MediaFileFilterer,
	aggregator Aggregator,
	disk FileSizeReader,
	sampler SampleClassifier,
	pathParser EpisodeInfoParser,
	tracked TrackedDownloadFinder,
	formats EpisodeCustomFormatCalculator,
	log *slog.Logger,
) (*ImportDecisionMaker, error) {
	if filter == nil {
		return nil, errors.New("episodeimport: NewImportDecisionMaker: nil MediaFileFilterer")
	}
	if aggregator == nil {
		return nil, errors.New("episodeimport: NewImportDecisionMaker: nil Aggregator")
	}
	if disk == nil {
		return nil, errors.New("episodeimport: NewImportDecisionMaker: nil FileSizeReader")
	}
	if sampler == nil {
		return nil, errors.New("episodeimport: NewImportDecisionMaker: nil SampleClassifier")
	}
	if pathParser == nil {
		pathParser = defaultEpisodeInfoParser{}
	}
	if log == nil {
		log = slog.Default()
	}
	// specs may legitimately be empty (every file is approved by
	// default when no specs registered; matches C# IoC behaviour
	// when zero IImportDecisionEngineSpecification implementations
	// are bound). We still defensively clone to insulate from caller
	// mutation.
	out := &ImportDecisionMaker{
		filter:     filter,
		aggregator: aggregator,
		disk:       disk,
		sampler:    sampler,
		parser:     pathParser,
		tracked:    tracked,
		formats:    formats,
		log:        log,
	}
	if len(specs) > 0 {
		out.specs = append([]Specification(nil), specs...)
	}
	return out, nil
}

// GetImportDecisionsOptions carries the optional parameters
// [ImportDecisionMaker.GetImportDecisions] takes. Replaces the C# four-
// way method overload at `ImportDecisionMaker.cs:54-69` with a single
// options struct: zero-value struct matches the simplest C# overload
// (nil DownloadItem / nil download-client info / nil folder info /
// SceneSource=false / FilterExistingFiles=false).
//
// FilterExistingFiles defaults to false because the zero-value struct
// shouldn't trigger a database hit by accident — explicit opt-in.
// The C# 2-arg overload at `:54-57` ALSO defaults to false; the 3-arg
// overload at `:59-62` defaults to true; the 6-arg overload at `:64-67`
// also defaults to true. Callers that want the C# defaults set the
// flag explicitly.
type GetImportDecisionsOptions struct {
	// DownloadItem is the source download-client item this batch
	// came from. nil for manual / library-rescan flows.
	DownloadItem *downloadclients.Item

	// DownloadItemInfo is the parse of the download client's
	// reported title (the original NZB / torrent name). nil for
	// manual / library-rescan flows.
	DownloadItemInfo *parser.ParsedEpisodeInfo

	// FolderInfo is the parse of the containing folder name when
	// the file lives one or more directories deep under the
	// download root. nil when the file sits at the download-root
	// top level.
	FolderInfo *parser.ParsedEpisodeInfo

	// SceneSource is true when the files came from a scene-named
	// download (vs a renamed file). Drives sample detection +
	// LocalEpisode.SceneSource.
	SceneSource bool

	// FilterExistingFiles toggles the
	// MediaFileFilterer.FilterExistingFiles pre-pass. true matches
	// the C# overloads at `:59-62` + `:64-67`; false matches the
	// 2-arg overload at `:54-57` (used by manual-import flow which
	// already filtered upstream).
	FilterExistingFiles bool
}

// GetImportDecisions ports `ImportDecisionMaker.cs:69-99` —
// the public entry point. Walks every videoFile, constructs a
// LocalEpisode, runs the per-file decision pipeline, returns the
// list of decisions in input order.
//
// Files filtered out by FilterExistingFiles are NOT in the returned
// slice (matching C# `:71` which substitutes newFiles for videoFiles
// in the loop). videoFiles is not mutated.
//
// Errors from FilterExistingFiles propagate; the C# version
// propagates the underlying SQL exception for the same reason. Per-
// file failures DO NOT propagate — they're recorded as Rejected
// decisions per `:158-167`.
//
// ctx threads to FilterExistingFiles + IsSamplePath. The per-spec
// IsSatisfiedBy currently doesn't take context because the existing
// Specification interface predates this slice; specs that need a
// context (SameEpisodesImportSpecification, etc.) carry a context
// supplier inside the spec itself.
func (m *ImportDecisionMaker) GetImportDecisions(
	ctx context.Context,
	videoFiles []string,
	s *series.Series,
	opts GetImportDecisionsOptions,
) ([]*ImportDecision, error) {
	if m == nil {
		return nil, errors.New("episodeimport: GetImportDecisions on nil receiver")
	}
	if s == nil {
		return nil, errors.New("episodeimport: GetImportDecisions: nil series")
	}

	var (
		newFiles []string
		err      error
	)
	if opts.FilterExistingFiles {
		newFiles, err = m.filter.FilterExistingFiles(ctx, append([]string(nil), videoFiles...), s)
		if err != nil {
			return nil, fmt.Errorf("episodeimport: FilterExistingFiles: %w", err)
		}
	} else {
		newFiles = append([]string(nil), videoFiles...)
	}

	m.log.Debug("episodeimport: analyzing files",
		"new", len(newFiles), "total", len(videoFiles))

	// Sample-pass count: the C# orchestrator only walks the sampler
	// when SceneSource=true (`:77`); otherwise it just uses the raw
	// videoFiles count to drive OtherVideoFiles. This is the
	// "avoid running ffprobe on every library-rescan file" fast path.
	var nonSampleVideoFileCount int
	if opts.SceneSource {
		nonSampleVideoFileCount = m.countNonSampleVideoFiles(ctx, newFiles, s, opts.DownloadItemInfo, opts.FolderInfo)
	} else {
		nonSampleVideoFileCount = len(videoFiles)
	}

	otherVideoFiles := nonSampleVideoFileCount > 1
	decisions := make([]*ImportDecision, 0, len(newFiles))
	for _, file := range newFiles {
		le := NewLocalEpisode()
		le.Series = s
		le.DownloadClientEpisodeInfo = opts.DownloadItemInfo
		le.DownloadItem = opts.DownloadItem
		le.FolderEpisodeInfo = opts.FolderInfo
		le.Path = file
		le.SceneSource = opts.SceneSource
		le.ExistingFile = ospath.IsParent(s.Path, file)
		le.OtherVideoFiles = otherVideoFiles

		if d := m.getDecisionForFile(le, opts.DownloadItem); d != nil {
			decisions = append(decisions, d)
		}
	}

	return decisions, nil
}

// GetDecision ports `ImportDecisionMaker.cs:101-107` — the public
// per-LocalEpisode decision path. Skips the parse / size / aggregation
// stages and goes straight to the spec evaluation. Used by the manual-
// import flow which already filled in the LocalEpisode upstream.
//
// Returns a non-nil ImportDecision either way: zero rejections →
// approved; one or more → rejected. nil le returns nil (the C#
// version would NPE — the Go port chooses defensive nil-return).
func (m *ImportDecisionMaker) GetDecision(le *LocalEpisode, item *downloadclients.Item) *ImportDecision {
	if m == nil || le == nil {
		return nil
	}
	rejections := m.evaluateSpecs(le, item)
	return NewImportDecision(le, rejections...)
}

// getDecisionForFile is the per-file C# `:109-183` path — the private
// 3-arg GetDecision overload that does the parse / size / aggregation
// stages before delegating to the public GetDecision for spec
// evaluation. Returns nil only when the catch arms produce nil
// (defensive — the C# version logs `:170-172` "Unable to make a
// decision" in that case; we mirror).
// _ context.Context parameter intentionally absent — the per-file
// pipeline currently has no ctx-taking dependencies (parser.ParsePath,
// FileSizeReader, Aggregator, TrackedDownloadFinder, formats are all
// ctx-less). The batch entrypoint threads ctx into the sampler +
// FilterExistingFiles which DO need it. Re-add when an aggregator
// gains a ctx-bearing dep.
func (m *ImportDecisionMaker) getDecisionForFile(
	le *LocalEpisode,
	item *downloadclients.Item,
) (decision *ImportDecision) {
	// recover() catches an aggregator / parser panic and turns it
	// into a Rejected decision. The C# version doesn't recover from
	// the unmanaged AccessViolation tier but DOES catch generic
	// Exception at `:162-167`; Go panics map to that arm.
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("episodeimport: panic during per-file decision",
				"path", le.Path, "panic", r)
			decision = NewImportDecision(le, NewImportRejection(
				ImportRejectionError, "Unexpected error processing file"))
		}
		if decision == nil {
			m.log.Error("episodeimport: unable to make a decision", "path", le.Path)
			return
		}
		if len(decision.Rejections) > 0 {
			m.log.Debug("episodeimport: file rejected",
				"path", le.Path, "rejections", joinRejections(decision.Rejections))
		} else {
			m.log.Debug("episodeimport: file accepted", "path", le.Path)
		}
	}()

	// Stage 1: parse the path + read file size.
	fileEpisodeInfo := m.parser.ParsePath(le.Path)
	le.FileEpisodeInfo = fileEpisodeInfo

	size, err := m.disk.GetFileSize(le.Path)
	if err != nil {
		m.log.Error("episodeimport: failed to read file size",
			"path", le.Path, "err", err)
		return NewImportDecision(le, NewImportRejection(
			ImportRejectionError, "Unexpected error processing file"))
	}
	le.Size = size

	// Stage 2: resolve ReleaseType using the DownloadClient → Folder →
	// File priority chain at C# `:119-123`.
	le.ReleaseType = pickReleaseType(le)

	// Stage 3: walk the aggregator chain.
	if _, err := m.aggregator.Augment(le, item); err != nil {
		if errors.Is(err, ErrAugmentingFailed) {
			return NewImportDecision(le, NewImportRejection(
				ImportRejectionUnableToParse, "Unable to parse file"))
		}
		m.log.Error("episodeimport: failed to augment local episode",
			"path", le.Path, "err", err)
		return NewImportDecision(le, NewImportRejection(
			ImportRejectionError, "Unexpected error processing file"))
	}

	// Stage 4: empty-Episodes triage. Mirrors C# `:126-140`.
	if len(le.Episodes) == 0 {
		switch {
		case isPartialSeason(le):
			return NewImportDecision(le, NewImportRejection(
				ImportRejectionPartialSeason, "Partial season packs are not supported"))
		case isSeasonExtra(le):
			return NewImportDecision(le, NewImportRejection(
				ImportRejectionSeasonExtra, "Extras are not supported"))
		default:
			return NewImportDecision(le, NewImportRejection(
				ImportRejectionInvalidSeasonOrEpisode, "Invalid season or episode"))
		}
	}

	// Stage 5: download-client enrichment. Mirrors C# `:143-153`.
	if item != nil && strings.TrimSpace(item.DownloadID) != "" {
		if m.tracked != nil {
			if flags, ok := m.tracked.FindIndexerFlags(item.DownloadID); ok {
				le.IndexerFlags = flags
			}
		}
	}
	if m.formats != nil {
		m.formats.UpdateEpisodeCustomFormats(le)
	}

	// Stage 6: spec evaluation.
	rejections := m.evaluateSpecs(le, item)
	return NewImportDecision(le, rejections...)
}

// evaluateSpecs walks every registered spec, collects rejections.
// Per-spec panics map to a DecisionError rejection rather than
// propagating — mirrors C# `:185-205` catch arm.
func (m *ImportDecisionMaker) evaluateSpecs(le *LocalEpisode, item *downloadclients.Item) []ImportRejection {
	if len(m.specs) == 0 {
		return nil
	}
	out := make([]ImportRejection, 0, len(m.specs))
	for _, spec := range m.specs {
		if r, ok := m.evaluateSpec(spec, le, item); ok {
			out = append(out, r)
		}
	}
	return out
}

// evaluateSpec runs one spec under a panic guard. Returns
// (rejection, true) when the spec rejected; (zero, false) when it
// accepted. Mirrors `ImportDecisionMaker.cs:185-205`.
func (m *ImportDecisionMaker) evaluateSpec(spec Specification, le *LocalEpisode, item *downloadclients.Item) (rej ImportRejection, fired bool) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("episodeimport: panic in spec",
				"spec", spec.Name(), "path", le.Path, "panic", r)
			rej = NewImportRejection(ImportRejectionDecisionError,
				fmt.Sprintf("%s: %v", spec.Name(), r))
			fired = true
		}
	}()
	d := spec.IsSatisfiedBy(le, item)
	if d == nil || d.Accepted() {
		return ImportRejection{}, false
	}
	return NewImportRejection(d.Reason(), d.Message()), true
}

// countNonSampleVideoFiles ports `ImportDecisionMaker.cs:207-225`.
// Returns the count of files that the sampler did NOT classify as
// Sample. Indeterminate counts as not-a-sample (matches C#'s default
// arm at `:222-223`).
func (m *ImportDecisionMaker) countNonSampleVideoFiles(
	ctx context.Context,
	files []string,
	s *series.Series,
	dcInfo *parser.ParsedEpisodeInfo,
	folderInfo *parser.ParsedEpisodeInfo,
) int {
	// C# `:209-212`: special-flag is download-client info OR folder
	// info. Either flagging "looks like a special" propagates so the
	// sampler skips runtime classification on file paths it would
	// otherwise misclassify (a 2-minute special isn't a sample).
	isPossibleSpecial := dcInfo != nil && dcInfo.IsPossibleSpecialEpisode()
	if !isPossibleSpecial && folderInfo != nil && folderInfo.IsPossibleSpecialEpisode() {
		isPossibleSpecial = true
	}

	expectedRuntime := 0
	if s != nil {
		expectedRuntime = s.Runtime
	}

	count := 0
	for _, file := range files {
		if m.sampler.IsSamplePath(ctx, file, isPossibleSpecial, expectedRuntime) == DetectSampleSample {
			continue
		}
		count++
	}
	return count
}

// pickReleaseType resolves [LocalEpisode.ReleaseType] from the three
// info sources at C# `:119-123` — download-client info has highest
// priority; folder info next; file info last; Unknown if all are nil.
//
// Pulled out as a free function so the orchestrator's stage-2
// implementation reads in declaration order and the helper is unit-
// testable in isolation.
func pickReleaseType(le *LocalEpisode) parser.ReleaseType {
	if le.DownloadClientEpisodeInfo != nil {
		if rt := le.DownloadClientEpisodeInfo.ReleaseType(); rt != parser.ReleaseTypeUnknown {
			return rt
		}
	}
	if le.FolderEpisodeInfo != nil {
		if rt := le.FolderEpisodeInfo.ReleaseType(); rt != parser.ReleaseTypeUnknown {
			return rt
		}
	}
	if le.FileEpisodeInfo != nil {
		if rt := le.FileEpisodeInfo.ReleaseType(); rt != parser.ReleaseTypeUnknown {
			return rt
		}
	}
	return parser.ReleaseTypeUnknown
}

// isPartialSeason ports `ImportDecisionMaker.cs:227-249`. Returns true
// when ANY of the three info sources flags IsPartialSeason. Order
// (DC → Folder → File) doesn't matter for the OR; mirrors C# field
// reads.
func isPartialSeason(le *LocalEpisode) bool {
	if le.DownloadClientEpisodeInfo != nil && le.DownloadClientEpisodeInfo.IsPartialSeason {
		return true
	}
	if le.FolderEpisodeInfo != nil && le.FolderEpisodeInfo.IsPartialSeason {
		return true
	}
	if le.FileEpisodeInfo != nil && le.FileEpisodeInfo.IsPartialSeason {
		return true
	}
	return false
}

// isSeasonExtra ports `ImportDecisionMaker.cs:251-273`. Same OR-shape
// as [isPartialSeason] over the three info sources.
func isSeasonExtra(le *LocalEpisode) bool {
	if le.DownloadClientEpisodeInfo != nil && le.DownloadClientEpisodeInfo.IsSeasonExtra {
		return true
	}
	if le.FolderEpisodeInfo != nil && le.FolderEpisodeInfo.IsSeasonExtra {
		return true
	}
	if le.FileEpisodeInfo != nil && le.FileEpisodeInfo.IsSeasonExtra {
		return true
	}
	return false
}

// joinRejections renders an []ImportRejection as a comma-separated
// string of `[<kind>] <message>` entries. Used by the per-file decision
// log line at `:175`.
func joinRejections(rejs []ImportRejection) string {
	if len(rejs) == 0 {
		return ""
	}
	parts := make([]string, len(rejs))
	for i, r := range rejs {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

// Compile-time interface-satisfaction guards. A refactor that breaks
// the contract (e.g. SampleDetector loses IsSamplePath, AggregationService
// changes its Augment signature) fails at build time rather than at
// the wiring callsite.
var (
	_ Aggregator        = (*AggregationService)(nil)
	_ SampleClassifier  = (*SampleDetector)(nil)
	_ EpisodeInfoParser = defaultEpisodeInfoParser{}
)
