package episodefilemoving

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodeimport"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/mediainfo"
)

// ExecProcessRunner is the production [ProcessRunner]. Wraps
// os/exec.CommandContext with environment-variable injection. Mirrors
// C# `ProcessProvider.StartAndCapture(executable, args, env)` —
// blocks until the process exits, captures stdout (stderr is logged
// through the wrapped error on non-zero exit). Goroutine-safe; each
// call spawns a fresh subprocess.
type ExecProcessRunner struct{}

// NewExecProcessRunner constructs a runner with no per-instance state.
// The package-level singleton is fine; this returns a fresh value
// purely for the construction-symmetry with [NewExecRunner] in
// videofileinforeader.
func NewExecProcessRunner() ExecProcessRunner {
	return ExecProcessRunner{}
}

// StartAndCapture executes the supplied script with the args
// positional + env environment dictionary. The os/exec environment
// is the *additional* env on top of the inherited parent — matching
// the C# StringDictionary semantics where the parent process env is
// inherited by default. err carries any stderr text on a non-zero
// exit so the caller's debug log surfaces the script's diagnostic.
func (ExecProcessRunner) StartAndCapture(ctx context.Context, executable string, args []string, env map[string]string) (string, int, error) {
	if strings.TrimSpace(executable) == "" {
		return "", 0, errors.New("episodefilemoving.ExecProcessRunner: empty executable path")
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	parent := cmd.Environ()
	merged := append([]string(nil), parent...)
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}
	cmd.Env = merged
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
		return stdout.String(), exitCode, fmt.Errorf("episodefilemoving.ExecProcessRunner: %s %v: %w (stderr=%q)", executable, args, err, stderr.String())
	}
	return stdout.String(), 0, nil
}

// ImportScriptConfigReader narrows [config.Provider] to the four
// keys ImportScriptService reads. Mirrors the C# IConfigService /
// IConfigFileProvider read surface at `ScriptImportDecider.cs:120,131,200,202`.
//
// nil-tolerant on the Provider side; this interface itself rejects
// nil at NewImportScriptService construction.
type ImportScriptConfigReader interface {
	UseScriptImport() bool
	ScriptImportPath() string
	ApplicationUrl() string
}

// InstanceNameReader narrows [configfile.Provider] to the
// "InstanceName" lookup. Production wiring binds an adapter that
// calls `*configfile.Provider.String(ctx, "InstanceName", "Sonarr",
// false)`; tests pass a stub.
type InstanceNameReader interface {
	InstanceName() string
}

// TagLabelReader narrows [tags.Service.GetByID] — the only tag-side
// surface ImportScriptService touches. nil-error / unknown-tag
// degrades to the empty-string label (the C# tree throws on a
// missing tag; the Go-port softens since a missing tag mid-import
// shouldn't tank the move).
type TagLabelReader interface {
	GetByID(ctx context.Context, id int) (label string, ok bool)
}

// ProcessRunner narrows os/exec to the one method ImportScriptService
// reaches for. Defined as an interface so tests can stub the exec
// layer with canned stdout / exit codes (the spike toolchain has no
// usable script binary). Production wiring binds [ExecProcessRunner]
// (see [NewExecProcessRunner]).
//
// args slice is positional; env is the StringDictionary the C# tree
// passes via `_processProvider.StartAndCapture`. On non-zero exit
// the runner SHOULD return a typed error including the exit code so
// the service can format the matching `ScriptImportException` log
// line.
type ProcessRunner interface {
	StartAndCapture(ctx context.Context, executable string, args []string, env map[string]string) (stdout string, exitCode int, err error)
}

// ImportScriptService ports `ScriptImportDecider.ImportScriptService`
// at `MediaFiles/ScriptImportDecider.cs:23-237`. Closes queue [15]
// (slice 3.BL). Replaces the slice-3.70
// alwaysDeferImportScriptRunner placeholder at the cmd/sonarr boot
// site so production deployments now actually exec the user-supplied
// import script and honour its move-decision verdict.
//
// Goroutine-safe: stateless after construction.
type ImportScriptService struct {
	cfg          ImportScriptConfigReader
	instanceName InstanceNameReader
	tags         TagLabelReader
	disk         FileExister
	media        MediaInfoReader
	proc         ProcessRunner
	log          *slog.Logger
}

// FileExister narrows the disk-existence check the script-output
// validator gates on. Mirrors C#
// `IDiskProvider.FileExists` at `:76`.
type FileExister interface {
	FileExists(path string) bool
}

// MediaInfoReader narrows the [videofileinforeader.Reader] surface
// the RenameRequested arm reaches for at C# `:230`. Returning a nil
// model is treated as "no media info" — matches the post-port
// behaviour of [videofileinforeader.Reader.GetMediaInfo].
type MediaInfoReader interface {
	GetMediaInfo(ctx context.Context, path string) (*mediainfo.Model, error)
}

// NewImportScriptService binds the seven narrow deps. nil-required-dep
// returns a typed error.
func NewImportScriptService(
	cfg ImportScriptConfigReader,
	instanceName InstanceNameReader,
	tags TagLabelReader,
	disk FileExister,
	media MediaInfoReader,
	proc ProcessRunner,
	log *slog.Logger,
) (*ImportScriptService, error) {
	if cfg == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: cfg is nil")
	}
	if instanceName == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: instanceName is nil")
	}
	if tags == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: tags is nil")
	}
	if disk == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: disk is nil")
	}
	if media == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: media is nil")
	}
	if proc == nil {
		return nil, errors.New("episodefilemoving: NewImportScriptService: proc is nil")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ImportScriptService{
		cfg:          cfg,
		instanceName: instanceName,
		tags:         tags,
		disk:         disk,
		media:        media,
		proc:         proc,
		log:          log,
	}, nil
}

// Compile-time guard — *ImportScriptService satisfies [ImportScriptRunner].
var _ ImportScriptRunner = (*ImportScriptService)(nil)

// scriptOutputRegex ports the C# regex at `ScriptImportDecider.cs:50`.
// Matches one of:
//
//	[MediaFile]<path>           → file replaces the destination
//	[ExtraFile]<path>           → companion file appended
//	[PreventExtraImport]        → suppress extras
//	[MoveStatus] DeferMove|MoveComplete|RenameRequested
//
// The named groups exactly match the C# names so the parser logic
// reads identical line-by-line.
var scriptOutputRegex = regexp.MustCompile(
	`^(?:\[(?:(?P<mediaFile>MediaFile)|(?P<extraFile>ExtraFile))\]\s?(?P<fileName>.+)|(?P<preventExtraImport>\[PreventExtraImport\])|\[MoveStatus\]\s?(?:(?P<deferMove>DeferMove)|(?P<moveComplete>MoveComplete)|(?P<renameRequested>RenameRequested)))$`,
)

// scriptImportInfo ports `ScriptImportInfo` (the inner record at C#
// `:110`). Carries the parsed result of one script invocation
// pre-mutation.
type scriptImportInfo struct {
	possibleExtraFiles []string
	mediaFile          string
	decision           ScriptImportDecision
	importExtraFiles   bool
}

// TryImport ports `ScriptImportService.TryImport` at `:113-235`.
//
// Non-nil-receiver / non-nil-le / non-nil-file are caller invariants
// — the slice-3.70 [Service] writer always passes them. nil-receiver
// returns DeferMove (the safest default — matches the
// alwaysDeferImportScriptRunner posture).
//
// Errors during script execution (nil-script-path, non-zero exit,
// invalid output) are logged at warn and surface as DeferMove —
// matches the slice-3.70 caller expectation that a script failure
// should NOT block the move (the writer falls back to its regular
// TransferFile path).
//
// Mutations applied to file / le on success:
//   - file.RelativePath = scriptInfo.mediaFile relative to series.Path
//   - le.PossibleExtraFiles = scriptInfo.possibleExtraFiles
//   - le.ShouldImportExtras = scriptInfo.importExtraFiles
//   - le.ScriptImported = true (unless decision == DeferMove)
//   - On RenameRequested: file.MediaInfo = media.GetMediaInfo(...)
//
// The C# tree also stamps `episodeFile.Path = mediaFile`; the Go-port
// File doesn't surface a Path field, so the path is encoded purely in
// RelativePath. Documented divergence at the field assignment site.
func (s *ImportScriptService) TryImport(src, dst string, le *episodeimport.LocalEpisode, file *episodefiles.File, mode TransferMode) ScriptImportDecision {
	if s == nil || le == nil || file == nil {
		return ScriptImportDeferMove
	}
	if !s.cfg.UseScriptImport() {
		return ScriptImportDeferMove
	}
	if le.Series == nil {
		s.log.Warn("episodefilemoving.ImportScriptService: nil series on LocalEpisode")
		return ScriptImportDeferMove
	}
	scriptPath := s.cfg.ScriptImportPath()
	if strings.TrimSpace(scriptPath) == "" {
		s.log.Warn("episodefilemoving.ImportScriptService: empty ScriptImportPath; deferring move")
		return ScriptImportDeferMove
	}
	ctx := context.Background()
	env := s.buildEnvironment(ctx, src, dst, mode, le)

	s.log.Debug("episodefilemoving.ImportScriptService: executing script", "path", scriptPath, "src", src, "dst", dst)

	args := []string{src, dst}
	stdout, exitCode, err := s.proc.StartAndCapture(ctx, scriptPath, args, env)
	if err != nil {
		s.log.Warn("episodefilemoving.ImportScriptService: script invocation failed", "err", err, "exit", exitCode)
		return ScriptImportDeferMove
	}
	s.log.Debug("episodefilemoving.ImportScriptService: script output", "stdout", stdout)
	if exitCode != 0 {
		s.log.Warn("episodefilemoving.ImportScriptService: non-zero exit", "exit", exitCode)
		return ScriptImportDeferMove
	}

	info, err := s.processOutput(stdout)
	if err != nil {
		s.log.Warn("episodefilemoving.ImportScriptService: invalid script output", "err", err)
		return ScriptImportDeferMove
	}

	mediaFile := info.mediaFile
	if mediaFile == "" {
		mediaFile = dst
	}
	le.PossibleExtraFiles = append([]string(nil), info.possibleExtraFiles...)

	rel, err := filepath.Rel(le.Series.Path, mediaFile)
	if err != nil {
		s.log.Warn("episodefilemoving.ImportScriptService: failed to relativise mediaFile", "mediaFile", mediaFile, "seriesPath", le.Series.Path, "err", err)
		return ScriptImportDeferMove
	}
	file.RelativePath = rel
	le.ShouldImportExtras = info.importExtraFiles
	if info.decision != ScriptImportDeferMove {
		le.ScriptImported = true
	}
	if info.decision == ScriptImportRenameRequested {
		// Pull live MediaInfo for the renamed file. Errors / nil
		// return collapse to "no media info" without aborting the
		// rename verdict — mirrors C# `:230` which would crash on a
		// nil reader but the Go-port's reader contract returns
		// (nil, nil) on probe failure.
		if mi, mediaErr := s.media.GetMediaInfo(ctx, mediaFile); mediaErr == nil {
			file.MediaInfo = mi
		} else {
			s.log.Debug("episodefilemoving.ImportScriptService: media probe error after rename", "err", mediaErr)
		}
	}
	return info.decision
}

// processOutput ports `ScriptImportService.ProcessOutput` at `:52-111`.
// Returns an error when the script's stdout is malformed (multiple
// MediaFile lines, MediaFile with unsupported extension, MediaFile
// pointing at a non-existent path). ExtraFile lines that point at a
// non-existent path log+continue (matches C# warning at `:87`).
func (s *ImportScriptService) processOutput(stdout string) (*scriptImportInfo, error) {
	info := &scriptImportInfo{
		decision:         ScriptImportMoveComplete,
		importExtraFiles: true,
	}
	for _, line := range splitLines(stdout) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := scriptOutputRegex.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		named := namedGroups(scriptOutputRegex, match)
		switch {
		case named["mediaFile"] != "":
			if info.mediaFile != "" {
				return nil, fmt.Errorf("episodefilemoving.ImportScriptService: script output contains multiple media files")
			}
			fileName := named["fileName"]
			ext := strings.ToLower(filepath.Ext(fileName))
			if !mediafiles.IsMediaExtension(ext) {
				return nil, fmt.Errorf("episodefilemoving.ImportScriptService: invalid media extension: %s", fileName)
			}
			if !s.disk.FileExists(fileName) {
				return nil, fmt.Errorf("episodefilemoving.ImportScriptService: non-existent media file: %s", fileName)
			}
			info.mediaFile = fileName
		case named["extraFile"] != "":
			fileName := named["fileName"]
			if !s.disk.FileExists(fileName) {
				s.log.Warn("episodefilemoving.ImportScriptService: non-existent extra file", "fileName", fileName)
			}
			info.possibleExtraFiles = append(info.possibleExtraFiles, fileName)
		case named["preventExtraImport"] != "":
			info.importExtraFiles = false
		case named["moveComplete"] != "":
			info.decision = ScriptImportMoveComplete
		case named["renameRequested"] != "":
			info.decision = ScriptImportRenameRequested
		case named["deferMove"] != "":
			info.decision = ScriptImportDeferMove
		}
	}
	return info, nil
}

// buildEnvironment ports the C# StringDictionary build at `:127-198`.
// Field-for-field — identical key names so existing user scripts
// continue to work unchanged.
func (s *ImportScriptService) buildEnvironment(ctx context.Context, src, dst string, mode TransferMode, le *episodeimport.LocalEpisode) map[string]string {
	env := map[string]string{}
	env["Sonarr_SourcePath"] = src
	env["Sonarr_DestinationPath"] = dst
	env["Sonarr_InstanceName"] = s.instanceName.InstanceName()
	env["Sonarr_ApplicationUrl"] = s.cfg.ApplicationUrl()
	env["Sonarr_TransferMode"] = mode.String()

	if ser := le.Series; ser != nil {
		env["Sonarr_Series_Id"] = strconv.Itoa(ser.ID)
		env["Sonarr_Series_Title"] = ser.Title
		env["Sonarr_Series_TitleSlug"] = ser.TitleSlug
		env["Sonarr_Series_Path"] = ser.Path
		env["Sonarr_Series_TvdbId"] = strconv.Itoa(ser.TvdbID)
		env["Sonarr_Series_TvMazeId"] = strconv.Itoa(ser.TvMazeID)
		env["Sonarr_Series_TmdbId"] = strconv.Itoa(ser.TmdbID)
		env["Sonarr_Series_ImdbId"] = ser.ImdbID
		env["Sonarr_Series_Type"] = seriesTypeName(ser.SeriesType)
		if lang, ok := language.FindByID(ser.OriginalLanguage); ok {
			if iso, ok := language.GetIso(lang); ok {
				env["Sonarr_Series_OriginalLanguage"] = iso.ThreeLetter
			}
		}
		// Series.Genres is stored as a pipe-separated string already
		// (see series.go); pass through verbatim.
		env["Sonarr_Series_Genres"] = ser.Genres
		labels := make([]string, 0, len(ser.Tags))
		for _, tagID := range ser.Tags {
			label, ok := s.tags.GetByID(ctx, tagID)
			if ok {
				labels = append(labels, label)
			}
		}
		env["Sonarr_Series_Tags"] = strings.Join(labels, "|")
	}

	env["Sonarr_EpisodeFile_EpisodeCount"] = strconv.Itoa(len(le.Episodes))
	env["Sonarr_EpisodeFile_EpisodeIds"] = joinEpisodeField(le.Episodes, ",", func(e episodes.Episode) string { return strconv.Itoa(e.ID) })
	env["Sonarr_EpisodeFile_SeasonNumber"] = seasonNumberString(le)
	env["Sonarr_EpisodeFile_EpisodeNumbers"] = joinEpisodeField(le.Episodes, ",", func(e episodes.Episode) string { return strconv.Itoa(e.EpisodeNumber) })
	env["Sonarr_EpisodeFile_EpisodeAirDates"] = joinEpisodeField(le.Episodes, ",", func(e episodes.Episode) string { return e.AirDate })
	env["Sonarr_EpisodeFile_EpisodeAirDatesUtc"] = joinEpisodeField(le.Episodes, ",", func(e episodes.Episode) string {
		if e.AirDateUtc == nil {
			return ""
		}
		return e.AirDateUtc.UTC().Format("2006-01-02T15:04:05Z")
	})
	env["Sonarr_EpisodeFile_EpisodeTitles"] = joinEpisodeField(le.Episodes, "|", func(e episodes.Episode) string { return e.Title })
	env["Sonarr_EpisodeFile_EpisodeOverviews"] = joinEpisodeField(le.Episodes, "|", func(e episodes.Episode) string { return e.Overview })
	env["Sonarr_EpisodeFile_Quality"] = le.Quality.Quality.Name
	env["Sonarr_EpisodeFile_QualityVersion"] = strconv.Itoa(le.Quality.Revision.Version)
	env["Sonarr_EpisodeFile_ReleaseGroup"] = le.ReleaseGroup
	env["Sonarr_EpisodeFile_SceneName"] = le.SceneName

	if le.DownloadItem != nil {
		if le.DownloadItem.DownloadClientInfo != nil {
			env["Sonarr_Download_Client"] = le.DownloadItem.DownloadClientInfo.Name
			env["Sonarr_Download_Client_Type"] = le.DownloadItem.DownloadClientInfo.Type
		}
		env["Sonarr_Download_Id"] = le.DownloadItem.DownloadID
	} else {
		env["Sonarr_Download_Client"] = ""
		env["Sonarr_Download_Client_Type"] = ""
		env["Sonarr_Download_Id"] = ""
	}

	if le.MediaInfo != nil {
		mi := le.MediaInfo
		env["Sonarr_EpisodeFile_MediaInfo_AudioCodec"] = mediainfo.FormatAudioCodec(mi.PrimaryAudioStream(), "")
		audioLanguages := []string{}
		seen := map[string]struct{}{}
		for _, a := range mi.AudioStreams {
			if _, dup := seen[a.Language]; dup {
				continue
			}
			seen[a.Language] = struct{}{}
			audioLanguages = append(audioLanguages, a.Language)
		}
		env["Sonarr_EpisodeFile_MediaInfo_AudioLanguages"] = strings.Join(audioLanguages, " / ")
		allLanguages := make([]string, 0, len(mi.AudioStreams))
		for _, a := range mi.AudioStreams {
			allLanguages = append(allLanguages, a.Language)
		}
		env["Sonarr_EpisodeFile_MediaInfo_Languages"] = strings.Join(allLanguages, " / ")
		env["Sonarr_EpisodeFile_MediaInfo_Height"] = strconv.Itoa(mi.Height)
		env["Sonarr_EpisodeFile_MediaInfo_Width"] = strconv.Itoa(mi.Width)
		subs := make([]string, 0, len(mi.SubtitleStreams))
		for _, s := range mi.SubtitleStreams {
			subs = append(subs, s.Language)
		}
		env["Sonarr_EpisodeFile_MediaInfo_Subtitles"] = strings.Join(subs, " / ")
		env["Sonarr_EpisodeFile_MediaInfo_VideoCodec"] = mediainfo.FormatVideoCodec(mi, "")
		env["Sonarr_EpisodeFile_MediaInfo_VideoDynamicRangeType"] = mediainfo.FormatVideoDynamicRangeType(mi)
	}

	cfNames := make([]string, 0, len(le.CustomFormats))
	for _, cf := range le.CustomFormats {
		cfNames = append(cfNames, cf.Name)
	}
	env["Sonarr_EpisodeFile_CustomFormat"] = strings.Join(cfNames, "|")
	env["Sonarr_EpisodeFile_CustomFormatScore"] = strconv.Itoa(le.CustomFormatScore)

	if len(le.OldFiles) > 0 && le.Series != nil {
		relPaths := make([]string, 0, len(le.OldFiles))
		fullPaths := make([]string, 0, len(le.OldFiles))
		dateAdded := make([]string, 0, len(le.OldFiles))
		recyclePaths := make([]string, 0, len(le.OldFiles))
		for _, of := range le.OldFiles {
			if of.File == nil {
				continue
			}
			rel := of.File.RelativePath
			relPaths = append(relPaths, rel)
			fullPaths = append(fullPaths, filepath.Join(le.Series.Path, rel))
			dateAdded = append(dateAdded, of.File.DateAdded.UTC().Format("2006-01-02T15:04:05Z"))
			recyclePaths = append(recyclePaths, of.RecycleBinPath)
		}
		env["Sonarr_DeletedRelativePaths"] = strings.Join(relPaths, "|")
		env["Sonarr_DeletedPaths"] = strings.Join(fullPaths, "|")
		env["Sonarr_DeletedDateAdded"] = strings.Join(dateAdded, "|")
		env["Sonarr_DeletedRecycleBinPaths"] = strings.Join(recyclePaths, "|")
	}
	return env
}

// joinEpisodeField mirrors the C# `string.Join(sep, eps.Select(...))`
// projections. Empty episode list → empty string.
func joinEpisodeField(eps []episodes.Episode, sep string, project func(episodes.Episode) string) string {
	if len(eps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(eps))
	for _, e := range eps {
		parts = append(parts, project(e))
	}
	return strings.Join(parts, sep)
}

// seasonNumberString uses [LocalEpisode.SeasonNumber] for the unique
// season check; on the rare error path (mismatched seasons across
// the episode list — which the writer rejects upstream anyway) the
// env var resolves to "" so the script sees an empty string rather
// than a misleading number.
func seasonNumberString(le *episodeimport.LocalEpisode) string {
	n, err := le.SeasonNumber()
	if err != nil {
		return ""
	}
	return strconv.Itoa(n)
}

// seriesTypeName renders the C# `series.SeriesType.ToString()` form
// — `"Standard"` / `"Daily"` / `"Anime"`. Mirrors the C# enum
// `Tv/SeriesTypes.cs`.
func seriesTypeName(t int) string {
	switch t {
	case 1:
		return "Daily"
	case 2:
		return "Anime"
	default:
		return "Standard"
	}
}

// splitLines tokenises the script's stdout into individual lines.
// Recognises both CRLF and LF (the C# tree's
// `processOutput.Lines` is a List<ProcessOutputLine> already split
// by line; the Go-port re-splits the captured string).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// namedGroups extracts named-capture groups from a regex match.
// Go's regexp returns positional matches; this helper walks
// SubexpNames() to map them back.
func namedGroups(r *regexp.Regexp, match []string) map[string]string {
	out := map[string]string{}
	for i, name := range r.SubexpNames() {
		if i == 0 || name == "" || i >= len(match) {
			continue
		}
		out[name] = match[i]
	}
	return out
}
