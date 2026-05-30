package extras

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/mediafiles"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
	"sonarr.tv/sonarr/internal/subtitlefiles"
)

// SubtitleStore persists + reads subtitle-file rows. Production binds an
// adapter over `*subtitlefiles.Repo`. Upsert inserts a fresh import row;
// AllBySeriesID + Update back the post-rename move pass. Mirrors C#
// `ISubtitleFileService` (Upsert / GetFilesBySeries).
type SubtitleStore interface {
	Upsert(ctx context.Context, f *subtitlefiles.SubtitleFile) error
	AllBySeriesID(ctx context.Context, seriesID int) ([]subtitlefiles.SubtitleFile, error)
	Update(ctx context.Context, f *subtitlefiles.SubtitleFile) error
}

// FolderVideoLister enumerates files under a folder. *disk.Provider
// satisfies it via GetFiles. Used ONLY by the single-video fallback;
// optional (nil → the fallback is skipped).
type FolderVideoLister interface {
	GetFiles(folder string, recursive bool) ([]string, error)
}

// SampleChecker reports whether a video file is a sample. Optional (nil →
// the single-video fallback treats every video as non-sample). Mirrors
// C# `IDetectSample.IsSample(...) == DetectSampleResult.Sample`.
type SampleChecker interface {
	IsSampleVideo(ser *series.Series, path string) bool
}

// SubtitleManager ports `Extras/Subtitles/SubtitleService.cs` (order 1).
// It claims subtitle-extension files and imports the ones belonging to
// the episode — by filename-prefix / season-episode match, or (when no
// file matches but the folder holds exactly one non-sample video) every
// subtitle. Imported subtitles are named with a language + copy +
// language-tag suffix.
type SubtitleManager struct {
	store   SubtitleStore
	t       FileTransferrer
	mover   FileMover
	cfg     HardlinkConfig
	folders FolderVideoLister // optional (single-video fallback)
	samples SampleChecker     // optional
	log     *slog.Logger
}

// NewSubtitleManager builds the manager. store / t / mover / cfg MUST be
// non-nil; folders + samples are optional (nil disables the single-video
// fallback). log defaults to [slog.Default].
func NewSubtitleManager(store SubtitleStore, t FileTransferrer, mover FileMover, cfg HardlinkConfig, folders FolderVideoLister, samples SampleChecker, log *slog.Logger) *SubtitleManager {
	if log == nil {
		log = slog.Default()
	}
	return &SubtitleManager{store: store, t: t, mover: mover, cfg: cfg, folders: folders, samples: samples, log: log}
}

// Order returns 1 (C# `SubtitleService.Order`).
func (*SubtitleManager) Order() int { return 1 }

// CanImportFile claims subtitle-extension files. Mirrors C#
// `SubtitleService.CanImportFile` (`:107-110`).
func (*SubtitleManager) CanImportFile(_ string, extension string) bool {
	return IsSubtitleExtension(extension)
}

type subRecord struct {
	path string
	lang language.Language
	ext  string
	tags []string
}

// ImportFiles ports `SubtitleService.ImportFiles` (`:112-239`).
func (m *SubtitleManager) ImportFiles(ctx context.Context, in ImportInput, files []string) (int, error) {
	if in.Series == nil || in.EpisodeFile == nil {
		return 0, nil
	}

	// Filter to subtitle extensions (`:116`).
	filtered := make([]string, 0, len(files))
	for _, f := range files {
		if IsSubtitleExtension(filepath.Ext(f)) {
			filtered = append(filtered, f)
		}
	}
	if len(filtered) == 0 {
		return 0, nil
	}

	sourceName := stemOf(in.SourcePath)

	// Match pass: filename-prefix or season/episode (`:127-150`).
	matching := make([]string, 0, len(filtered))
	for _, f := range filtered {
		if matchesEpisode(sourceName, in.FileInfo, f) {
			matching = append(matching, f)
		}
	}

	// Single-video fallback (`:153-186`): no file matched, but there is
	// exactly one non-sample video in the source folder → import any
	// available subtitle.
	if len(matching) == 0 && m.folders != nil && in.SourcePath != "" {
		if m.singleVideoInFolder(in.Series, filepath.Dir(in.SourcePath)) {
			matching = append(matching, filtered...)
			m.log.Warn("extras: imported any available subtitle (single video in folder)",
				"series", in.Series.Title, "folder", filepath.Dir(in.SourcePath))
		}
	}
	if len(matching) == 0 {
		return 0, nil
	}

	// Build per-file records (`:188-204`).
	records := make([]subRecord, 0, len(matching))
	for _, f := range matching {
		records = append(records, subRecord{
			path: f,
			lang: parser.ParseSubtitleLanguage(f),
			ext:  filepath.Ext(f),
			tags: parser.ParseLanguageTags(f),
		})
	}

	// Group by (language, tags, extension) preserving first-seen order
	// (C# `GroupBy(s => s.AggregateString)`, `:206`).
	order := make([]string, 0, len(records))
	groups := make(map[string][]subRecord)
	for _, r := range records {
		key := fmt.Sprintf("%d\x00%s\x00%s", r.lang.ID, strings.Join(r.tags, "\x00"), r.ext)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}

	// Import per group, numbering copies within a multi-copy group
	// (`:208-236`).
	count := 0
	for _, key := range order {
		g := groups[key]
		multi := len(g) > 1
		copyNo := 1
		for _, r := range g {
			suffix := getSuffix(r.lang, copyNo, r.tags, multi, "")
			rel, err := importFile(m.t, m.cfg.CopyUsingHardlinks(), in.Series.Path, in.EpisodeFile.RelativePath, r.path, suffix, r.ext, in.ReadOnly)
			if err != nil {
				m.log.Warn("extras: import subtitle failed", "file", r.path, "err", err)
				continue
			}
			row := &subtitlefiles.SubtitleFile{
				SeriesID:      in.Series.ID,
				SeasonNumber:  in.EpisodeFile.SeasonNumber,
				EpisodeFileID: in.EpisodeFile.ID,
				RelativePath:  rel,
				Extension:     r.ext,
				Language:      r.lang.ID,
				LanguageTags:  r.tags,
			}
			if err := m.store.Upsert(ctx, row); err != nil {
				m.log.Warn("extras: persist subtitle failed", "path", rel, "err", err)
				continue
			}
			count++
			copyNo++
		}
	}
	return count, nil
}

// singleVideoInFolder reports whether the folder holds exactly one
// non-sample video file. Mirrors C# `:156-185` (count media files;
// >2 → bail; drop samples; ==1 → true).
func (m *SubtitleManager) singleVideoInFolder(ser *series.Series, folder string) bool {
	all, err := m.folders.GetFiles(folder, true)
	if err != nil {
		m.log.Debug("extras: single-video fallback: GetFiles failed", "folder", folder, "err", err)
		return false
	}
	videos := make([]string, 0, len(all))
	for _, f := range all {
		if mediafiles.IsMediaExtension(filepath.Ext(f)) {
			videos = append(videos, f)
		}
	}
	// C# bails when more than two video files exist (ambiguous).
	if len(videos) > 2 {
		return false
	}
	if m.samples != nil {
		kept := videos[:0]
		for _, f := range videos {
			if !m.samples.IsSampleVideo(ser, f) {
				kept = append(kept, f)
			}
		}
		videos = kept
	}
	return len(videos) == 1
}

// getSuffix ports `SubtitleService.GetSuffix` (`:241-275`): an optional
// title (or, when no title, a copy number for multi-copy groups), the
// language's two-letter ISO code, then any language tags — each
// dot-prefixed. The ImportFiles path passes title="" (C# null); the
// post-rename path passes the persisted SubtitleFile.Title.
func getSuffix(lang language.Language, copyNo int, tags []string, multipleCopies bool, title string) string {
	var b strings.Builder
	if title != "" {
		b.WriteString(".")
		b.WriteString(title)
		if multipleCopies {
			b.WriteString(" - ")
			b.WriteString(strconv.Itoa(copyNo))
		}
	} else if multipleCopies {
		b.WriteString(".")
		b.WriteString(strconv.Itoa(copyNo))
	}
	if lang != language.Unknown {
		if iso, ok := language.GetIso(lang); ok && iso.TwoLetter != "" {
			b.WriteString(".")
			b.WriteString(iso.TwoLetter)
		}
	}
	if len(tags) > 0 {
		b.WriteString(".")
		b.WriteString(strings.Join(tags, "."))
	}
	return b.String()
}

// MoveFilesAfterRename ports `SubtitleService.MoveFilesAfterRename`. For
// each renamed episode file it groups that file's subtitle rows by
// (language, title, tags, ext), (re)numbers copies in a multi-copy group,
// recomputes the suffix, and moves each companion to the new name —
// persisting the updated RelativePath (+ Copy).
func (m *SubtitleManager) MoveFilesAfterRename(ctx context.Context, in MoveInput) (int, error) {
	if in.Series == nil || len(in.EpisodeFiles) == 0 {
		return 0, nil
	}
	all, err := m.store.AllBySeriesID(ctx, in.Series.ID)
	if err != nil {
		return 0, fmt.Errorf("extras: subtitle MoveFilesAfterRename: load: %w", err)
	}

	count := 0
	for _, ef := range in.EpisodeFiles {
		if ef == nil {
			continue
		}
		// Group this episode file's subtitle rows by AggregateString,
		// preserving first-seen order (C# `:212-215`).
		order := make([]string, 0)
		groups := make(map[string][]*subtitlefiles.SubtitleFile)
		for i := range all {
			row := &all[i]
			if row.EpisodeFileID != ef.ID {
				continue
			}
			key := fmt.Sprintf("%d\x00%s\x00%s\x00%s", row.Language, row.Title, strings.Join(row.LanguageTags, "\x00"), row.Extension)
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], row)
		}

		for _, key := range order {
			g := groups[key]
			multi := len(g) > 1
			copyNo := g[0].Copy // C# `group.First().Copy`
			ordered := append([]*subtitlefiles.SubtitleFile(nil), g...)
			sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Copy > ordered[j].Copy })
			for _, row := range ordered {
				if multi && row.Copy == 0 {
					copyNo++
					row.Copy = copyNo
				}
				lang, _ := language.FindByID(row.Language)
				suffix := getSuffix(lang, row.Copy, row.LanguageTags, multi, row.Title)
				rel, moved, err := moveFile(m.mover, in.Series.Path, ef.RelativePath, row.RelativePath, suffix, row.Extension)
				if err != nil {
					m.log.Warn("extras: move subtitle after rename failed", "path", row.RelativePath, "err", err)
					continue
				}
				if !moved {
					continue
				}
				row.RelativePath = rel
				if err := m.store.Update(ctx, row); err != nil {
					m.log.Warn("extras: persist moved subtitle failed", "id", row.ID, "err", err)
					continue
				}
				count++
			}
		}
	}
	return count, nil
}

// Compile-time interface satisfaction.
var _ Manager = (*SubtitleManager)(nil)
