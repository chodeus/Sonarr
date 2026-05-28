// Boot-time adapters for the J364 MediaCover cover-download subscriber.
// Bridge the production disk + http + config services into the narrow
// interfaces internal/mediacover declares, so the J363 download arm
// (EnsureCovers) can run off the series-mutation events.

package main

import (
	"context"
	"os"
	"path/filepath"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/mediacover"
)

// dotNetUnixEpochTicks is the .NET DateTime tick count (100ns intervals
// since 0001-01-01) at the Unix epoch (1970-01-01). Used to render the
// `?lastWrite=<ticks>` cache-buster the C# tree emits from
// `lastWrite.Ticks`.
const dotNetUnixEpochTicks = 621355968000000000

// mediaCoverFS adapts *disk.Provider to mediacover.Filesystem. The
// provider has no FileExists (os.Stat suffices) and its FileGetLastWrite
// returns a time.Time — converted here to .NET ticks.
type mediaCoverFS struct{ disk *disk.Provider }

func (f mediaCoverFS) FileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
func (f mediaCoverFS) FolderExists(p string) bool { return f.disk.FolderExists(p) }
func (f mediaCoverFS) DeleteFolder(p string, recursive bool) error {
	return f.disk.DeleteFolder(p, recursive)
}
func (f mediaCoverFS) GetFileSize(p string) (int64, error) { return f.disk.GetFileSize(p) }
func (f mediaCoverFS) FileGetLastWrite(p string) (int64, error) {
	t, err := f.disk.FileGetLastWrite(p)
	if err != nil {
		return 0, err
	}
	return t.UTC().UnixNano()/100 + dotNetUnixEpochTicks, nil
}

// mediaCoverFolders satisfies mediacover.AppFolders —
// `<AppData>/MediaCover` (mirrors C# `IAppFolderInfo.GetMediaCoverPath`).
type mediaCoverFolders struct{ appData string }

func (m mediaCoverFolders) MediaCoverPath() string { return filepath.Join(m.appData, "MediaCover") }

// mediaCoverURLBase satisfies mediacover.URLBaseReader via the
// configfile UrlBase. Errors collapse to "" (the no-prefix default).
type mediaCoverURLBase struct{ cfg *configfile.Provider }

func (u mediaCoverURLBase) URLBase() string {
	base, _ := u.cfg.URLBase(context.Background())
	return base
}

// mediaCoverDownloader satisfies mediacover.CoverDownloader over
// *httpclient.Client — a HEAD probe for Content-Length + a file
// download. Mirrors the IHttpClient slice the C# MediaCoverService +
// CoverAlreadyExistsSpecification reach for.
type mediaCoverDownloader struct{ client httpclient.Client }

func (d mediaCoverDownloader) ContentLength(ctx context.Context, url string) (int64, bool, error) {
	req, err := httpclient.NewRequest(url)
	if err != nil {
		return 0, false, err
	}
	resp, err := d.client.Head(ctx, req)
	if err != nil {
		return 0, false, err
	}
	return resp.Headers.ContentLength()
}

func (d mediaCoverDownloader) DownloadFile(ctx context.Context, url, path string) error {
	return d.client.DownloadFile(ctx, url, path)
}

// Compile-time guards.
var (
	_ mediacover.Filesystem      = mediaCoverFS{}
	_ mediacover.AppFolders      = mediaCoverFolders{}
	_ mediacover.URLBaseReader   = mediaCoverURLBase{}
	_ mediacover.CoverDownloader = mediaCoverDownloader{}
)
