// Package episodeimport is the Go port of
// `NzbDrone.Core.MediaFiles.EpisodeImport` — the per-file import
// pipeline that decides which on-disk video files become persisted
// EpisodeFile rows. The C# subsystem hangs together as:
//
//   - Parser.ParsePath(path) → ParsedEpisodeInfo (already ported).
//   - AggregationService walks 7 aggregators to fill in
//     Episodes / Languages / Quality / ReleaseGroup / ReleaseHash /
//     ReleaseInfo / SubtitleInfo on the LocalEpisode value type.
//   - 14 IImportDecisionEngineSpecification specs each return Accept
//     or Reject(reason, message); the orchestrator collects all
//     rejections and labels the file Approved if zero fired.
//   - ImportApprovedEpisodes finally moves files into the library,
//     mediated by IUpgradeMediaFiles for the recycle-bin / replace
//     flow.
//
// This slice (1 of an 8-slice arc — see STATUS.md DiskScanService
// stub-replacement follow-up) lands the data model and one
// interface; the machinery downstream slices fill in.
//
// What's NOT in this slice (intentionally deferred):
//
//   - LocalEpisode.ToEpisodeFile — the writer slice (8) is the only
//     caller and needs the full upgrade/recycle/IndexerFlags/grab-
//     history derivation logic to compose the persisted shape.
//     Pinning the helper here would freeze details that don't
//     actually settle until the writer ports.
//
//   - GrabbedReleaseInfo type — the C# class derives from history
//     rows. Lives in the history subsystem; ports alongside that.
//     Slice 1 uses `any` for [LocalEpisode.Release] so call sites
//     compile and downstream slices can substitute the real type
//     without churning every spec.
//
// Note on per-release vs per-import rejections: the existing
// `internal/decisionengine` package ports the per-release scoring
// pipeline (parsed remote release + matched series + episodes). Its
// Rejection type is bound to a different reason enum
// (decisionengine.RejectionReason vs episodeimport.ImportRejectionReason).
// The two never mix at runtime — per-release decisions go to the
// download pipeline; per-import decisions go to the file pipeline —
// so the Go port carries them as parallel hierarchies rather than
// a generic shared type. C# uses `Rejection<T>` for the same reason
// (no second instantiation in either subsystem).
package episodeimport

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediainfo"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

// ErrInvalidSeason is returned by [LocalEpisode.SeasonNumber] when
// the LocalEpisode has zero episodes OR carries episodes from
// multiple distinct seasons. Mirrors the C# `InvalidSeasonException`
// at `LocalEpisode.cs:57,62`.
//
// The C# version throws; the Go port returns a wrapped sentinel so
// the orchestrator can surface the rejection cleanly without panic
// recovery at every call site.
var ErrInvalidSeason = errors.New("episodeimport: invalid season")

// ImportRejectionReason is the wire-stable enum naming the
// distinct reasons a file fails to import. Mirrors
// `src/NzbDrone.Core/MediaFiles/EpisodeImport/ImportRejectionReason.cs:3-42`.
//
// Numeric order is frozen — the v3 React frontend serialises this as
// an int through SignalR and persists per-int reason filters in user
// preferences. Adding new entries APPENDS at the end; reordering or
// removing entries breaks existing user data.
type ImportRejectionReason int

// ImportRejectionReason values. Names + ordinals match the C# enum
// at `ImportRejectionReason.cs:5-41` exactly.
const (
	ImportRejectionUnknown                           ImportRejectionReason = iota // 0
	ImportRejectionFileLocked                                                     // 1
	ImportRejectionUnknownSeries                                                  // 2
	ImportRejectionDangerousFile                                                  // 3
	ImportRejectionExecutableFile                                                 // 4
	ImportRejectionUserRejectedExtension                                          // 5
	ImportRejectionArchiveFile                                                    // 6
	ImportRejectionSeriesFolder                                                   // 7
	ImportRejectionInvalidFilePath                                                // 8
	ImportRejectionUnsupportedExtension                                           // 9
	ImportRejectionPartialSeason                                                  // 10
	ImportRejectionSeasonExtra                                                    // 11
	ImportRejectionInvalidSeasonOrEpisode                                         // 12
	ImportRejectionUnableToParse                                                  // 13
	ImportRejectionError                                                          // 14
	ImportRejectionDecisionError                                                  // 15
	ImportRejectionNoEpisodes                                                     // 16
	ImportRejectionMissingAbsoluteEpisodeNumber                                   // 17
	ImportRejectionEpisodeAlreadyImported                                         // 18
	ImportRejectionTitleMissing                                                   // 19
	ImportRejectionTitleTba                                                       // 20
	ImportRejectionMinimumFreeSpace                                               // 21
	ImportRejectionFullSeason                                                     // 22
	ImportRejectionNoAudio                                                        // 23
	ImportRejectionEpisodeUnexpected                                              // 24
	ImportRejectionEpisodeNotFoundInRelease                                       // 25
	ImportRejectionSample                                                         // 26
	ImportRejectionSampleIndeterminate                                            // 27
	ImportRejectionUnpacking                                                      // 28
	ImportRejectionExistingFileHasMoreEpisodes                                    // 29
	ImportRejectionSplitEpisode                                                   // 30
	ImportRejectionUnverifiedSceneMapping                                         // 31
	ImportRejectionNotQualityUpgrade                                              // 32
	ImportRejectionNotRevisionUpgrade                                             // 33
	ImportRejectionNotCustomFormatUpgrade                                         // 34
	ImportRejectionNotCustomFormatUpgradeAfterRename                              // 35
	ImportRejectionMultiSeason                                                    // 36
)

// importRejectionReasonNames carries the C# enum identifier for each
// ordinal so log lines + JSON serialisation match the C# tree's
// default `Enum.ToString()` shape. The order matches the const block
// above. Used by [ImportRejectionReason.String].
var importRejectionReasonNames = [...]string{
	"Unknown",
	"FileLocked",
	"UnknownSeries",
	"DangerousFile",
	"ExecutableFile",
	"UserRejectedExtension",
	"ArchiveFile",
	"SeriesFolder",
	"InvalidFilePath",
	"UnsupportedExtension",
	"PartialSeason",
	"SeasonExtra",
	"InvalidSeasonOrEpisode",
	"UnableToParse",
	"Error",
	"DecisionError",
	"NoEpisodes",
	"MissingAbsoluteEpisodeNumber",
	"EpisodeAlreadyImported",
	"TitleMissing",
	"TitleTba",
	"MinimumFreeSpace",
	"FullSeason",
	"NoAudio",
	"EpisodeUnexpected",
	"EpisodeNotFoundInRelease",
	"Sample",
	"SampleIndeterminate",
	"Unpacking",
	"ExistingFileHasMoreEpisodes",
	"SplitEpisode",
	"UnverifiedSceneMapping",
	"NotQualityUpgrade",
	"NotRevisionUpgrade",
	"NotCustomFormatUpgrade",
	"NotCustomFormatUpgradeAfterRename",
	"MultiSeason",
}

// String returns the C# enum-identifier form of the reason
// (e.g. `Unknown`, `EpisodeAlreadyImported`). Out-of-range values
// fall through to `ImportRejectionReason(N)` so log lines never
// silently swap a fresh-but-not-yet-named entry for `Unknown`.
func (r ImportRejectionReason) String() string {
	if int(r) >= 0 && int(r) < len(importRejectionReasonNames) {
		return importRejectionReasonNames[r]
	}
	return fmt.Sprintf("ImportRejectionReason(%d)", int(r))
}

// ImportResultType is the three-state outcome of an import attempt.
// Mirrors `ImportResultType.cs:1-9`.
//
// Numeric order is frozen — the v3 frontend deserialises this as an
// int from history rows.
type ImportResultType int

// ImportResultType values. Order matches C# `ImportResultType.cs:5-7`.
const (
	// ImportResultImported — the file was moved into the library
	// and a fresh EpisodeFile row was inserted.
	ImportResultImported ImportResultType = 0

	// ImportResultRejected — at least one specification rejected
	// the file. Errors slice is empty in this case (the rejection
	// reasons live on the ImportDecision).
	ImportResultRejected ImportResultType = 1

	// ImportResultSkipped — the decision was approved but the
	// writer raised an error (root folder missing, destination
	// already exists, recycle-bin failure). Errors slice carries
	// the per-attempt error messages.
	ImportResultSkipped ImportResultType = 2
)

// String returns the C# enum-identifier form for log lines.
func (r ImportResultType) String() string {
	switch r {
	case ImportResultImported:
		return "Imported"
	case ImportResultRejected:
		return "Rejected"
	case ImportResultSkipped:
		return "Skipped"
	default:
		return fmt.Sprintf("ImportResultType(%d)", int(r))
	}
}

// ImportMode mirrors `ImportMode.cs:1-9`. Decides whether the writer
// moves or copies the file into the library, with Auto deferring to
// the per-download-client `CanMoveFiles` flag.
type ImportMode int

// ImportMode values. Numeric order matches the C# enum at
// `ImportMode.cs:5-7`.
const (
	// ImportModeAuto — defer to DownloadClientItem.CanMoveFiles
	// (true → Move, false → Copy). The default for every C# call
	// site that doesn't override it.
	ImportModeAuto ImportMode = 0

	// ImportModeMove — always move the file regardless of
	// CanMoveFiles. Used by the manual-import flow when the user
	// explicitly clicked "Move".
	ImportModeMove ImportMode = 1

	// ImportModeCopy — always copy the file regardless of
	// CanMoveFiles. Used when the user wants the source preserved
	// (still seeding, etc.).
	ImportModeCopy ImportMode = 2
)

// String returns the C# enum-identifier form for log lines.
func (m ImportMode) String() string {
	switch m {
	case ImportModeAuto:
		return "Auto"
	case ImportModeMove:
		return "Move"
	case ImportModeCopy:
		return "Copy"
	default:
		return fmt.Sprintf("ImportMode(%d)", int(m))
	}
}

// EpisodeTitleRequiredType mirrors the C# enum at
// `src/NzbDrone.Core/MediaFiles/EpisodeImport/EpisodeTitleRequiredType.cs:3-8`.
// Drives [EpisodeTitleSpecification]'s "skip / always reject / only
// reject for bulk-season releases" gating. The persisted form is the
// enum identifier (`"Always"` / `"BulkSeasonReleases"` / `"Never"`),
// matching C# `GetValueEnum` storage at
// `Configuration/ConfigService.cs:494-496`.
type EpisodeTitleRequiredType int

// EpisodeTitleRequiredType values. Numeric order frozen — the API
// surface serialises this as int.
const (
	// EpisodeTitleRequiredAlways — every recently-aired episode
	// without a title rejects the import.
	EpisodeTitleRequiredAlways EpisodeTitleRequiredType = 0

	// EpisodeTitleRequiredBulkSeasonReleases — only reject when the
	// import looks like a same-day bulk-season release. Single-file
	// imports for individual episodes pass.
	EpisodeTitleRequiredBulkSeasonReleases EpisodeTitleRequiredType = 1

	// EpisodeTitleRequiredNever — disable the spec entirely.
	EpisodeTitleRequiredNever EpisodeTitleRequiredType = 2
)

// String returns the C# enum-identifier form. Matches the persisted
// shape used by [ParseEpisodeTitleRequiredType] for round-trips.
func (t EpisodeTitleRequiredType) String() string {
	switch t {
	case EpisodeTitleRequiredAlways:
		return "Always"
	case EpisodeTitleRequiredBulkSeasonReleases:
		return "BulkSeasonReleases"
	case EpisodeTitleRequiredNever:
		return "Never"
	default:
		return fmt.Sprintf("EpisodeTitleRequiredType(%d)", int(t))
	}
}

// ParseEpisodeTitleRequiredType resolves a persisted enum-identifier
// (case-insensitive) back to the typed value. Mirrors the C# `Enum.Parse`
// path at `Configuration/ConfigService.cs:494-496` (the GetValueEnum
// helper passes `ignoreCase=true`).
//
// An empty string OR an unrecognised value returns
// [EpisodeTitleRequiredAlways] — matches the C# default at
// `ConfigService.cs:260` (`GetValueEnum("EpisodeTitleRequired",
// EpisodeTitleRequiredType.Always)`).
func ParseEpisodeTitleRequiredType(s string) EpisodeTitleRequiredType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "always", "0":
		return EpisodeTitleRequiredAlways
	case "bulkseasonreleases", "1":
		return EpisodeTitleRequiredBulkSeasonReleases
	case "never", "2":
		return EpisodeTitleRequiredNever
	default:
		return EpisodeTitleRequiredAlways
	}
}

// ImportRejection is one named reason a file failed an import spec.
// Mirrors `ImportRejection.cs:5-11`. Same shape as
// [decisionengine.Rejection] (Reason, Message, Kind) but typed for
// ImportRejectionReason — the two pipelines never share rejection
// lists so a single generic type would buy nothing.
//
// Direct field access is allowed; both factories preserve the
// invariant that Kind is set.
type ImportRejection struct {
	// Reason is the categorical rejection reason. Routed to the
	// v3 API and used by the React frontend to look up a localised
	// template.
	Reason ImportRejectionReason
	// Message is the human-readable rendering of Reason, with
	// per-rejection parameters interpolated. Spec-side ports build
	// this via fmt.Sprintf at the rejection site.
	Message string
	// Kind tags the rejection as Permanent (drop) or Temporary
	// (re-queue for retry).
	Kind decisionengine.RejectionKind
}

// NewImportRejection builds a Permanent ImportRejection. Mirrors the
// C# 2-arg constructor at `ImportRejection.cs:7-9`
// (default RejectionType=Permanent).
func NewImportRejection(reason ImportRejectionReason, message string) ImportRejection {
	return ImportRejection{Reason: reason, Message: message, Kind: decisionengine.RejectionPermanent}
}

// NewImportRejectionWithKind builds an ImportRejection with the
// supplied [decisionengine.RejectionKind]. Used by specs that surface
// a Temporary rejection (file still being written, free-space check
// pending a re-stat).
func NewImportRejectionWithKind(reason ImportRejectionReason, message string, kind decisionengine.RejectionKind) ImportRejection {
	return ImportRejection{Reason: reason, Message: message, Kind: kind}
}

// String returns the C# `Rejection.ToString` shape: `[<kind>] <message>`.
// Same format the per-release [decisionengine.Rejection.String]
// produces so log lines look uniform.
func (r ImportRejection) String() string {
	return "[" + r.Kind.String() + "] " + r.Message
}

// ImportSpecDecision is the per-spec verdict — Accept singleton or
// Reject(reason, message) factory output. Mirrors
// `ImportSpecDecision.cs:1-33`.
//
// Returned by [Specification.IsSatisfiedBy]. Pointer-typed so the
// global Accept singleton can be allocated once at package init and
// shared across every spec that returns it.
type ImportSpecDecision struct {
	accepted bool
	reason   ImportRejectionReason
	message  string
}

// importAcceptDecision is the shared package-private singleton.
// Mirrors C# `private static readonly ImportSpecDecision AcceptDecision`
// at `ImportSpecDecision.cs:9`.
var importAcceptDecision = &ImportSpecDecision{accepted: true}

// AcceptImportSpec returns the shared accept singleton. Specs whose
// IsSatisfiedBy concludes "no objection" return this value — same
// pointer per call so the orchestrator's identity-compare path
// short-circuits the rejection-construction work.
func AcceptImportSpec() *ImportSpecDecision { return importAcceptDecision }

// RejectImportSpec builds a Reject decision. message is rendered as
// printf-format with args. Mirrors the C# overload at
// `ImportSpecDecision.cs:19-22`.
func RejectImportSpec(reason ImportRejectionReason, message string, args ...any) *ImportSpecDecision {
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	return &ImportSpecDecision{
		accepted: false,
		reason:   reason,
		message:  message,
	}
}

// Accepted returns true when the spec voted Accept.
func (d *ImportSpecDecision) Accepted() bool {
	if d == nil {
		return false
	}
	return d.accepted
}

// Reason returns the rejection reason. Zero value (ImportRejectionUnknown)
// for accept decisions.
func (d *ImportSpecDecision) Reason() ImportRejectionReason {
	if d == nil {
		return ImportRejectionUnknown
	}
	return d.reason
}

// Message returns the rejection message. Empty for accept decisions.
func (d *ImportSpecDecision) Message() string {
	if d == nil {
		return ""
	}
	return d.message
}

// ImportDecision is the orchestrator output for one candidate file —
// the LocalEpisode + every rejection that fired. Mirrors
// `ImportDecision.cs:8-20`.
//
// Approved iff Rejections is empty. The orchestrator collects every
// rejection (not just the first) so the UI can surface the full list
// to the user.
type ImportDecision struct {
	LocalEpisode *LocalEpisode
	Rejections   []ImportRejection
}

// NewImportDecision builds an ImportDecision with the supplied
// rejections. Variadic so the zero-rejection (approved) construction
// stays at the call site `NewImportDecision(le)`. Defensive copy of
// the rejections slice so caller mutations don't leak.
func NewImportDecision(le *LocalEpisode, rejections ...ImportRejection) *ImportDecision {
	out := &ImportDecision{LocalEpisode: le}
	if len(rejections) > 0 {
		out.Rejections = slices.Clone(rejections)
	}
	return out
}

// Approved returns true when zero rejections fired. Mirrors the C#
// derived property at `ImportDecision.cs:13`.
func (d *ImportDecision) Approved() bool {
	return d != nil && len(d.Rejections) == 0
}

// ImportResult is the per-attempt outcome the writer produces — the
// decision + the resulting EpisodeFile + any errors that surfaced
// during the move/copy. Mirrors `ImportResult.cs:7-47`.
//
// ResultType derivation: empty errors → Imported, rejected decision
// with errors → Rejected, approved decision with errors → Skipped.
// Matches the C# `ImportResultType` getter at `ImportResult.cs:13-29`.
type ImportResult struct {
	Decision    *ImportDecision
	EpisodeFile *episodefiles.File
	Errors      []string
}

// NewSkippedImportResult builds an ImportResult for a file the
// writer attempted but couldn't import (root-folder missing,
// destination conflict, recycle-bin failure). Mirrors the
// errors-only C# constructor at `ImportResult.cs:31-37`.
//
// Rejects nil decision because the C# version `Ensure.That.IsNotNull`s
// at `:33`; surfacing the misuse loudly at the call site is safer
// than letting a downstream method blow up on the nil deref.
func NewSkippedImportResult(decision *ImportDecision, errors ...string) *ImportResult {
	if decision == nil {
		panic("episodeimport: nil decision in NewSkippedImportResult")
	}
	out := &ImportResult{Decision: decision}
	if len(errors) > 0 {
		out.Errors = slices.Clone(errors)
	}
	return out
}

// NewImportedImportResult builds an ImportResult for a successful
// import. Mirrors the file-only C# constructor at
// `ImportResult.cs:39-46`.
func NewImportedImportResult(decision *ImportDecision, file *episodefiles.File) *ImportResult {
	if decision == nil {
		panic("episodeimport: nil decision in NewImportedImportResult")
	}
	return &ImportResult{Decision: decision, EpisodeFile: file, Errors: []string{}}
}

// ResultType derives the three-state outcome from the result fields.
// Matches the C# `Result` getter at `ImportResult.cs:13-29` exactly:
//
//   - errors empty → Imported.
//   - errors non-empty + decision approved → Skipped.
//   - errors non-empty + decision rejected → Rejected.
func (r *ImportResult) ResultType() ImportResultType {
	if r == nil {
		return ImportResultImported
	}
	if len(r.Errors) == 0 {
		return ImportResultImported
	}
	if r.Decision != nil && r.Decision.Approved() {
		return ImportResultSkipped
	}
	return ImportResultRejected
}

// Specification is the Go-port equivalent of C#
// `IImportDecisionEngineSpecification` at
// `IImportDecisionEngineSpecification.cs:6-9`. Each concrete spec
// vetoes (or doesn't) on a single LocalEpisode candidate; the
// orchestrator collects every rejection that fires and assembles the
// final ImportDecision.
//
// downloadClientItem may be nil for specs evaluated outside the
// download-client-driven flow (manual import, library rescan).
// Specs that need it MUST nil-check defensively — the C# tree
// returns Accept on nil paths.
//
// Name is a Go-port addition: C# discovers spec names via reflection
// over the IoC registration. Go ports register specs via a slice
// passed to the orchestrator constructor; an explicit Name keeps log
// lines stable when an interface adapter wraps a spec.
type Specification interface {
	// IsSatisfiedBy returns Accept when the candidate passes the
	// spec, or Reject(reason, msg) when it fails. Returning nil is
	// undefined behaviour — every spec must return one of the two.
	IsSatisfiedBy(localEpisode *LocalEpisode, downloadClientItem *downloadclients.Item) *ImportSpecDecision

	// Name returns the human-readable name surfaced in log lines
	// (e.g. "FullSeasonSpecification"). The orchestrator falls
	// back to a reflection-derived name when this returns empty.
	Name() string
}

// LocalEpisode is the central per-file value the orchestrator hands
// to every Specification. Mirrors
// `src/NzbDrone.Core/Parser/Model/LocalEpisode.cs:15-150`.
//
// Most fields are zero-defaulted; the orchestrator + Aggregation
// pipeline fills them in stages:
//
//   - Path / Series / SceneSource / ExistingFile / OtherVideoFiles /
//     DownloadItem / DownloadClientEpisodeInfo / FolderEpisodeInfo
//     are set by the orchestrator before running specs.
//   - FileEpisodeInfo / Size / ReleaseType are set by the
//     orchestrator's per-file `Parser.ParsePath` + `disk.GetFileSize`
//     pass (slice 7).
//   - Episodes / Languages / Quality / ReleaseGroup / ReleaseHash /
//     Release / SubtitleInfo / MediaInfo / IndexerFlags are set by
//     the Aggregation subsystem (slice 6).
//   - CustomFormats / CustomFormatScore / OriginalFileNameCustomFormats
//     are set by the LocalEpisodeCustomFormatCalculationService
//     (slice 7).
//
// Slice fields default to non-nil zero-length so range loops + len()
// don't need nil guards. Mirrors the C# field initialisers at
// `LocalEpisode.cs:24,27,37,39`.
//
// Ported into the episodeimport package rather than parser because
// the type carries cross-domain references (series / episodes /
// episodefiles / customformats / mediainfo / downloadclients) that
// would invert the parser package's existing leaf-dependency posture.
// Every consumer is in episodeimport already, so the package
// boundary follows usage.
type LocalEpisode struct {
	// Path is the absolute on-disk path of the candidate file.
	// Required. Used as the primary key for log lines.
	Path string

	// Size is the file size in bytes. Filled by the orchestrator's
	// `GetFileSize` call before running specs.
	Size int64

	// FileEpisodeInfo is `Parser.ParsePath(Path)`'s output.
	FileEpisodeInfo *parser.ParsedEpisodeInfo

	// DownloadClientEpisodeInfo is the parse of the download
	// client's reported title (the original NZB / torrent name).
	// nil for manual / library-rescan flows.
	DownloadClientEpisodeInfo *parser.ParsedEpisodeInfo

	// DownloadItem is the source download-client item this file
	// came from. nil for manual / library-rescan flows.
	DownloadItem *downloadclients.Item

	// FolderEpisodeInfo is the parse of the containing folder name
	// when the file lives one or more directories deep under the
	// download root. nil when the file sits at the download-root
	// top level.
	FolderEpisodeInfo *parser.ParsedEpisodeInfo

	// Series is the matched series this file belongs to. Required.
	Series *series.Series

	// Episodes is the matched-episode list for this file. Filled
	// by AggregateEpisodes; empty for files the orchestrator
	// couldn't map (these get rejected with one of the
	// PartialSeason / SeasonExtra / InvalidSeasonOrEpisode
	// reasons).
	Episodes []episodes.Episode

	// OldFiles is populated by ImportApprovedEpisodes during the
	// upgrade flow — every pre-existing EpisodeFile that's being
	// recycle-binned gets recorded here so the EpisodeImportedEvent
	// downstream consumers (history writer, notification dispatcher)
	// can see what was replaced.
	OldFiles []episodefiles.DeletedFile

	// Quality is the parsed quality model. Filled by
	// AggregateQuality.
	Quality quality.Model

	// Languages is the parsed language list. Filled by
	// AggregateLanguage.
	Languages []language.Language

	// IndexerFlags is the per-release flag set carried over from
	// the originating grab (Freeleech, Halfleech, etc.).
	IndexerFlags parser.IndexerFlags

	// ReleaseType is the parsed release type. Filled by the
	// orchestrator's three-source resolve at C#
	// `ImportDecisionMaker.cs:119-123`.
	ReleaseType parser.ReleaseType

	// MediaInfo is the embedded MediaInfo blob. Filled by
	// AggregateMediaInfo.
	MediaInfo *mediainfo.Model

	// ExistingFile is true when the file already lives inside
	// Series.Path (library rescan flow) rather than under a
	// download-client output directory.
	ExistingFile bool

	// SceneSource is true when the file came from a scene-named
	// download (vs a renamed file). Drives DetectSample's
	// is-this-a-real-sample-or-just-named-sample heuristic.
	SceneSource bool

	// ReleaseGroup is the parsed release group. Filled by
	// AggregateReleaseGroup.
	ReleaseGroup string

	// ReleaseHash is the parsed crc32 / hash suffix. Filled by
	// AggregateReleaseHash.
	ReleaseHash string

	// SceneName is the canonical scene name, if any. Filled by
	// SceneNameCalculator (slice 2).
	SceneName string

	// OtherVideoFiles is true when the orchestrator detected more
	// than one non-sample video file in the import batch. Drives a
	// few specs' "is this a season pack with multiple files" gates.
	OtherVideoFiles bool

	// CustomFormats is the matching-CustomFormat list for the
	// file. Filled by LocalEpisodeCustomFormatCalculationService.
	CustomFormats []customformats.CustomFormat

	// CustomFormatScore is the cached score for the
	// CustomFormats list under the candidate's Series QualityProfile.
	CustomFormatScore int

	// OriginalFileNameCustomFormats is the parallel list computed
	// from the original on-wire title (DownloadClientEpisodeInfo)
	// rather than the post-rename file name. Used by
	// NotCustomFormatUpgradeAfterRename.
	OriginalFileNameCustomFormats []customformats.CustomFormat

	// OriginalFileNameCustomFormatScore is the cached score for
	// OriginalFileNameCustomFormats.
	OriginalFileNameCustomFormatScore int

	// Release is the resolved grabbed-release info from history
	// (GrabbedReleaseInfo in C#). nil when the import isn't
	// download-client-driven. The Go port defers the full type
	// port to the history subsystem; this slice carries `any` so
	// call sites compile and downstream slices can substitute the
	// real type without churning every spec.
	Release any

	// ScriptImported is true when an external import script
	// claimed responsibility for this file (the script-import
	// flow). The writer skips its built-in extras handling when
	// the script handled it.
	ScriptImported bool

	// FileNameBeforeRename carries the pre-rename file path the
	// import script saw, so the post-script extras handler can
	// move companions correctly.
	FileNameBeforeRename string

	// FileNameUsedForCustomFormatCalculation pins the exact name
	// the CustomFormat calculation evaluated, which may differ
	// from Path when the orchestrator passed a temporary rename
	// hint.
	FileNameUsedForCustomFormatCalculation string

	// ShouldImportExtras is true when the writer should run the
	// extras-import pass alongside the main file move.
	ShouldImportExtras bool

	// PossibleExtraFiles carries the candidate companion paths the
	// extras importer should consider.
	PossibleExtraFiles []string

	// SubtitleInfo carries the parsed subtitle metadata
	// (language, title, hi/cc flags). Filled by
	// AggregateSubtitleInfo.
	SubtitleInfo *parser.SubtitleTitleInfo
}

// NewLocalEpisode constructs a LocalEpisode with the slice fields
// initialised to non-nil zero-length so callers can range without
// guarding for nil. Mirrors the C# default field initialisers at
// `LocalEpisode.cs:24,27,37,39`.
func NewLocalEpisode() *LocalEpisode {
	return &LocalEpisode{
		Episodes:                      []episodes.Episode{},
		Languages:                     []language.Language{},
		CustomFormats:                 []customformats.CustomFormat{},
		OriginalFileNameCustomFormats: []customformats.CustomFormat{},
	}
}

// SeasonNumber returns the unique season across LocalEpisode.Episodes.
// Mirrors the C# getter at `LocalEpisode.cs:49-67` — throws
// InvalidSeasonException on zero or multi-season inputs.
//
// The Go port returns a sentinel error wrapping [ErrInvalidSeason]
// rather than panicking so the orchestrator can surface the
// rejection cleanly.
func (l *LocalEpisode) SeasonNumber() (int, error) {
	if l == nil || len(l.Episodes) == 0 {
		return 0, fmt.Errorf("%w: expected one season, found none", ErrInvalidSeason)
	}
	first := l.Episodes[0].SeasonNumber
	for _, e := range l.Episodes[1:] {
		if e.SeasonNumber != first {
			return 0, fmt.Errorf("%w: expected one season, found multiple", ErrInvalidSeason)
		}
	}
	return first, nil
}

// IsSpecial mirrors the C# getter at `LocalEpisode.cs:69`. Returns
// true iff SeasonNumber == 0. Returns false on the error path
// (multi-season / no-episodes) — the orchestrator nil-checks the
// SeasonNumber return separately.
func (l *LocalEpisode) IsSpecial() bool {
	if l == nil {
		return false
	}
	n, err := l.SeasonNumber()
	if err != nil {
		return false
	}
	return n == 0
}

// String mirrors the C# `ToString()` at `LocalEpisode.cs:71-74` —
// returns the Path. Used by orchestrator log lines.
func (l *LocalEpisode) String() string {
	if l == nil {
		return ""
	}
	return l.Path
}

// GetSceneOrFileName mirrors the C# helper at `LocalEpisode.cs:76-89`.
// Returns SceneName if set; otherwise the file basename without
// extension; otherwise the empty string.
//
// The Go port uses [strings.LastIndexAny] to strip the path prefix
// + extension rather than `System.IO.Path.GetFileNameWithoutExtension`
// (which is platform-shape-locked in the .NET BCL). Same end-result
// for both shape flavours — mirrors `internal/ospath.Base` semantics.
func (l *LocalEpisode) GetSceneOrFileName() string {
	if l == nil {
		return ""
	}
	if strings.TrimSpace(l.SceneName) != "" {
		return l.SceneName
	}
	if strings.TrimSpace(l.Path) == "" {
		return ""
	}
	base := l.Path
	if idx := strings.LastIndexAny(base, `\/`); idx >= 0 {
		base = base[idx+1:]
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	return base
}
