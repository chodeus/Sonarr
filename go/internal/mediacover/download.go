package mediacover

import (
	"context"

	"sonarr.tv/sonarr/internal/series"
)

// HandleSeriesUpdated ports the `IHandleAsync<SeriesUpdatedEvent>` arm
// of C# `MediaCoverService.HandleAsync` at
// `MediaCover/MediaCoverService.cs:233-238` — ensures every cover the
// series carries is downloaded to disk, returning whether any cover was
// actually fetched this pass (the C# `updated` flag the
// MediaCoversUpdatedEvent carries). The caller (the bus subscriber)
// publishes [CoversUpdatedEvent] with the returned flag.
//
// nil receiver / nil series / nil downloader → (false, nil): the
// download arm is a no-op until a [CoverDownloader] is wired (matches
// the pre-J363 posture where MediaCover only did URL rewriting).
func (s *Service) HandleSeriesUpdated(ctx context.Context, ser *series.Series) (bool, error) {
	if s == nil || ser == nil {
		return false, nil
	}
	return s.EnsureCovers(ctx, ser)
}

// EnsureCovers ports `MediaCoverService.EnsureCovers` at `:110-166`
// (the download half; the ImageSharp resize half stays deferred — see
// the package doc-comment). For each cover the series carries (skipping
// CoverTypeUnknown), it downloads the remote image to its on-disk path
// when the local copy is missing or stale (size mismatch). Returns true
// when at least one cover was downloaded.
//
// Per-cover download failures are logged + absorbed (the cover is left
// for the next refresh) — matching the C# try/catch around each cover
// at `:125-146`, so one bad URL doesn't abort the whole series' cover
// sync.
//
// nil downloader → (false, nil): no download attempted.
func (s *Service) EnsureCovers(ctx context.Context, ser *series.Series) (bool, error) {
	if s == nil || ser == nil || s.downloader == nil {
		return false, nil
	}
	covers, err := DecodeSeriesImages(ser)
	if err != nil {
		// A malformed Images blob shouldn't abort the refresh; log +
		// treat as "no covers".
		s.log.Warn("mediacover: decode series images failed; skipping cover download",
			"seriesId", ser.ID, "err", err)
		return false, nil
	}
	updated := false
	for i := range covers {
		cover := covers[i]
		if cover.CoverType == CoverTypeUnknown {
			continue
		}
		path := s.GetCoverPath(ser.ID, cover.CoverType, 0)
		if s.coverAlreadyExists(ctx, cover.RemoteUrl, path) {
			continue
		}
		if err := s.downloadCover(ctx, ser, cover, path); err != nil {
			s.log.Warn("mediacover: couldn't download cover",
				"seriesId", ser.ID, "coverType", cover.CoverType.String(),
				"remoteUrl", cover.RemoteUrl, "err", err)
			continue
		}
		updated = true
	}
	return updated, nil
}

// coverAlreadyExists ports `CoverAlreadyExistsSpecification.AlreadyExists`
// at `MediaCover/CoverAlreadyExistsSpecification.cs:21-31` — a local
// cover is "already present" iff the file exists AND its size matches
// the remote Content-Length (a HEAD probe). Any probe failure (no
// downloader, HEAD error, missing/unparseable Content-Length, file-size
// read error) returns false → the cover is (re)downloaded, matching the
// C# behaviour where a header miss makes `fileSize == ContentLength`
// false.
func (s *Service) coverAlreadyExists(ctx context.Context, remoteURL, path string) bool {
	if s.downloader == nil || !s.fs.FileExists(path) {
		return false
	}
	remoteLen, ok, err := s.downloader.ContentLength(ctx, remoteURL)
	if err != nil || !ok {
		s.log.Debug("mediacover: HEAD probe failed; will re-download",
			"path", path, "remoteUrl", remoteURL, "err", err)
		return false
	}
	fileSize, err := s.fs.GetFileSize(path)
	if err != nil {
		return false
	}
	return fileSize == remoteLen
}

// downloadCover ports `MediaCoverService.DownloadCover` at `:168-174`.
func (s *Service) downloadCover(ctx context.Context, ser *series.Series, cover Cover, path string) error {
	s.log.Info("mediacover: downloading cover",
		"seriesId", ser.ID, "coverType", cover.CoverType.String(), "remoteUrl", cover.RemoteUrl)
	return s.downloader.DownloadFile(ctx, cover.RemoteUrl, path)
}
