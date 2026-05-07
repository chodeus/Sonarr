// DownloadedEpisodesScan handler. Ports the
// IExecute<DownloadedEpisodesScanCommand> implementation at
// `src/NzbDrone.Core/MediaFiles/DownloadedEpisodesCommandService.cs:1-91`.
//
// Routes a `{"name":"DownloadedEpisodesScan", "path": "...",
// "downloadClientId": "...", "importMode": 0|1|2}` API command into
// the [downloadedimport.Service] orchestrator that ports
// `DownloadedEpisodesImportService` (slices 3.79a→3.79d).
//
// # Optional dependencies
//
// CompletedDownloadVerifier (the C# `ICompletedDownloadService.VerifyImport`
// arm at `:58`) and CommandResultReporter (the C# `ICommandResultReporter.Report`
// arm at `:87`) are nil-tolerant — production wires real
// implementations once those subsystems port; the handler skips
// the corresponding branches when nil.
//
// TrackedDownloadFinder is also nil-tolerant — when nil OR when the
// command body has no `downloadClientId`, the handler falls through
// to the manual-import path (C# `:64-66`), invoking
// ImportProcessor.ProcessPath without a series/dci.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/trackeddownloads"
)

// DownloadedEpisodesScanName is the registry key + Commands.Name.
// Mirrors the C# short form: `DownloadedEpisodesScanCommand` →
// `DownloadedEpisodesScan`.
const DownloadedEpisodesScanName = "DownloadedEpisodesScan"

// ImportProcessor narrows [downloadedimport.Service.ProcessPath] —
// the one method this handler reaches for. Production wires
// `*downloadedimport.Service` via Go's structural typing.
type ImportProcessor interface {
	// ProcessPath dispatches a manual-import or download-client
	// import request. ser + dci may be nil (manual import, no
	// download-client item known).
	ProcessPath(
		path string,
		mode episodeimport.ImportMode,
		ser *series.Series,
		dci *downloadclients.Item,
	) []*episodeimport.ImportResult
}

// TrackedDownloadFinder narrows [trackeddownloads.Service.Find] (one
// method). Used by the C# `:50` `_trackedDownloadService.Find` arm.
// Returns nil when the downloadClientId doesn't match a known
// tracked download — mirrors the C# nil-return that the handler
// turns into the warn-then-fall-through-to-manual-import behaviour
// at `:62-64`.
//
// nil-tolerant: handler skips the lookup branch entirely when the
// finder is nil OR the command body has no downloadClientId.
type TrackedDownloadFinder interface {
	Find(downloadID string) *trackeddownloads.TrackedDownload
}

// CompletedDownloadVerifier narrows
// `ICompletedDownloadService.VerifyImport` (one method). Optional —
// when nil the handler skips the post-import verification branch
// at C# `:58`. Production wires the real subsystem once the
// download-client lifecycle ports.
type CompletedDownloadVerifier interface {
	VerifyImport(td *trackeddownloads.TrackedDownload, results []*episodeimport.ImportResult)
}

// CommandResultReporter narrows `ICommandResultReporter.Report` (one
// method). Optional — when nil the "no Imported result" arm at C#
// `:82-87` only logs and doesn't mark the command unsuccessful.
//
// Production binding wires the real subsystem when the command
// runtime gains a reporter API.
type CommandResultReporter interface {
	ReportUnsuccessful()
}

// DownloadedEpisodesScan is the IExecute<DownloadedEpisodesScanCommand>
// port. Holds a required ImportProcessor and three optional deps;
// at least the processor must be non-nil — ProcessPath has no
// meaningful nil fallback.
type DownloadedEpisodesScan struct {
	processor ImportProcessor
	finder    TrackedDownloadFinder
	verifier  CompletedDownloadVerifier
	reporter  CommandResultReporter
	log       *slog.Logger
}

// NewDownloadedEpisodesScan builds the handler. processor MUST be
// non-nil. finder / verifier / reporter are optional; nil disables
// the corresponding C# arms. log defaults to [slog.Default].
func NewDownloadedEpisodesScan(
	processor ImportProcessor,
	finder TrackedDownloadFinder,
	verifier CompletedDownloadVerifier,
	reporter CommandResultReporter,
	log *slog.Logger,
) (*DownloadedEpisodesScan, error) {
	if processor == nil {
		return nil, errors.New("handlers: NewDownloadedEpisodesScan: nil ImportProcessor")
	}
	if log == nil {
		log = slog.Default()
	}
	return &DownloadedEpisodesScan{
		processor: processor,
		finder:    finder,
		verifier:  verifier,
		reporter:  reporter,
		log:       log,
	}, nil
}

// downloadedEpisodesScanBody is the JSON shape of the command body.
// Mirrors the C# `DownloadedEpisodesScanCommand` properties at
// `Commands/DownloadedEpisodesScanCommand.cs:9-11`.
type downloadedEpisodesScanBody struct {
	Path             string `json:"path"`
	DownloadClientId string `json:"downloadClientId"`
	// ImportMode: 0=Auto, 1=Move, 2=Copy. Matches
	// [episodeimport.ImportMode] ordinals.
	ImportMode int `json:"importMode"`
}

// Execute parses the JSON command body and dispatches into the
// [ImportProcessor].
//
// Behaviour, matching C# `:69-89`:
//
//   - Empty/whitespace Path → ArgumentException equivalent: returns
//     an error so the dispatcher marks the command failed.
//     Mirrors C# `:79`.
//
//   - DownloadClientId set + finder wired + Find returns a
//     TrackedDownload → ProcessPath with that TrackedDownload's
//     Series + DownloadItem. Optional VerifyImport on success.
//
//   - DownloadClientId set + finder nil OR Find returns nil → log
//     warn + fall through to manual import (C# `:62-66`).
//
//   - DownloadClientId empty → manual import — ProcessPath with
//     nil series + nil dci.
//
//   - When no result has Result=Imported (or results is nil/empty)
//     log + report unsuccessful via [CommandResultReporter] when
//     wired. Mirrors C# `:82-87`.
func (h *DownloadedEpisodesScan) Execute(ctx context.Context, body string) error {
	cmd, err := parseDownloadedEpisodesScanBody(body)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cmd.Path) == "" {
		return errors.New("handlers: DownloadedEpisodesScan: a path must be provided")
	}

	importMode := episodeimport.ImportMode(cmd.ImportMode)

	results := h.dispatch(ctx, cmd, importMode)

	if !anyImported(results) {
		h.log.LogAttrs(ctx, slog.LevelInfo,
			"downloadedepisodesscan: failed to import",
			slog.String("command", DownloadedEpisodesScanName),
			slog.String("path", cmd.Path))
		if h.reporter != nil {
			h.reporter.ReportUnsuccessful()
		}
	}
	return nil
}

// dispatch routes to the tracked-download-aware path or the manual
// import path based on cmd.DownloadClientId + finder availability.
// Mirrors C# `ProcessPath` at `:40-67` exactly.
func (h *DownloadedEpisodesScan) dispatch(
	ctx context.Context,
	cmd downloadedEpisodesScanBody,
	importMode episodeimport.ImportMode,
) []*episodeimport.ImportResult {
	if cmd.DownloadClientId != "" && h.finder != nil {
		td := h.finder.Find(cmd.DownloadClientId)
		if td != nil {
			h.log.LogAttrs(ctx, slog.LevelDebug,
				"downloadedepisodesscan: external scan for known download",
				slog.String("download_client_id", cmd.DownloadClientId),
				slog.String("path", cmd.Path))
			var (
				ser *series.Series
				dci *downloadclients.Item
			)
			if td.RemoteEpisode != nil {
				ser = td.RemoteEpisode.Series
			}
			dci = td.DownloadItem
			results := h.processor.ProcessPath(cmd.Path, importMode, ser, dci)
			if h.verifier != nil {
				h.verifier.VerifyImport(td, results)
			}
			return results
		}
		h.log.LogAttrs(ctx, slog.LevelWarn,
			"downloadedepisodesscan: external scan for unknown download, falling through to manual import",
			slog.String("download_client_id", cmd.DownloadClientId),
			slog.String("path", cmd.Path))
	}
	return h.processor.ProcessPath(cmd.Path, importMode, nil, nil)
}

// parseDownloadedEpisodesScanBody parses the JSON body. Empty /
// whitespace bodies decode as the zero-value struct (Path="" — the
// caller's empty-path check fires). A malformed JSON body returns a
// wrapped error so the dispatcher classifies it as a programming
// error rather than a "no path provided" semantic failure.
func parseDownloadedEpisodesScanBody(body string) (downloadedEpisodesScanBody, error) {
	var out downloadedEpisodesScanBody
	body = strings.TrimSpace(body)
	if body == "" || body == "{}" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return out, fmt.Errorf("handlers: DownloadedEpisodesScan: parse body: %w", err)
	}
	return out, nil
}

// anyImported returns true when at least one result has
// Result=Imported. Mirrors the C# `importResults.All(v => v.Result
// != ImportResultType.Imported)` arm at `:82` — the negation of
// "none imported" is "any imported".
func anyImported(results []*episodeimport.ImportResult) bool {
	for _, r := range results {
		if r != nil && r.ResultType() == episodeimport.ImportResultImported {
			return true
		}
	}
	return false
}
