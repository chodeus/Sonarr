package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/httpclient"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadatafiles"
	"sonarr.tv/sonarr/internal/metadataservice"
	"sonarr.tv/sonarr/internal/recyclebin"
	"sonarr.tv/sonarr/internal/series"
)

// metadataDiskAdapter wraps *disk.Provider to satisfy
// [metadataservice.DiskWriter]. WriteAllText is implemented via
// os.WriteFile (disk.Provider doesn't expose a public text-writer
// today; this is the smallest seam that avoids modifying the shared
// provider).
type metadataDiskAdapter struct {
	provider *disk.Provider
}

func newMetadataDiskAdapter(p *disk.Provider) *metadataDiskAdapter {
	return &metadataDiskAdapter{provider: p}
}

func (a *metadataDiskAdapter) WriteAllText(path, contents string) error {
	// WriteAllText uses os.WriteFile directly — disk.Provider has no
	// public text-writer today, so we go around it. The provider
	// field is only required for FileExists / FolderExists / CopyFile /
	// MoveFile below; WriteAllText works without it.
	return os.WriteFile(path, []byte(contents), 0o644)
}

func (a *metadataDiskAdapter) FileExists(path string) bool {
	return a.provider.FileExists(path)
}

func (a *metadataDiskAdapter) FolderExists(path string) bool {
	return a.provider.FolderExists(path)
}

func (a *metadataDiskAdapter) CopyFile(src, dst string) error {
	return a.provider.CopyFile(src, dst)
}

func (a *metadataDiskAdapter) MoveFile(src, dst string) error {
	return a.provider.MoveFile(src, dst)
}

// metadataHTTPAdapter wraps httpclient.Client to satisfy
// [metadataservice.HTTPDownloader]. The wrapper exists only to bind
// the interface shape; the underlying DownloadFile call delegates.
type metadataHTTPAdapter struct {
	client httpclient.Client
}

func newMetadataHTTPAdapter(c httpclient.Client) *metadataHTTPAdapter {
	return &metadataHTTPAdapter{client: c}
}

func (a *metadataHTTPAdapter) DownloadFile(ctx context.Context, url, dst string) error {
	if a == nil || a.client == nil {
		return errors.New("metadataHTTPAdapter: nil client")
	}
	return a.client.DownloadFile(ctx, url, dst)
}

// metadataFileStoreAdapter wraps *metadatafiles.Repo to satisfy
// [metadataservice.MetadataFileStore]. The generic
// repo.Repository[T] handles Insert/Update/Delete already; this
// adapter adds Upsert (insert-or-update on f.ID==0) + threads
// FilesBySeries through the new queries.go helper.
type metadataFileStoreAdapter struct {
	repo *metadatafiles.Repo
}

func newMetadataFileStoreAdapter(r *metadatafiles.Repo) *metadataFileStoreAdapter {
	return &metadataFileStoreAdapter{repo: r}
}

func (a *metadataFileStoreAdapter) Insert(ctx context.Context, m *metadatafiles.MetadataFile) (int, error) {
	// repo.Repository[T].Insert returns (*T, error) but mutates m via
	// the mapper's SetID hook. The metadataservice contract returns
	// (int, error) — read m.ID after the call.
	if _, err := a.repo.Insert(ctx, m); err != nil {
		return 0, err
	}
	return m.ID, nil
}

func (a *metadataFileStoreAdapter) Update(ctx context.Context, m *metadatafiles.MetadataFile) error {
	return a.repo.Update(ctx, m)
}

func (a *metadataFileStoreAdapter) Delete(ctx context.Context, id int) error {
	return a.repo.Delete(ctx, id)
}

func (a *metadataFileStoreAdapter) FilesBySeries(ctx context.Context, seriesID int) ([]metadatafiles.MetadataFile, error) {
	return a.repo.AllBySeriesID(ctx, seriesID)
}

// Upsert dispatches on f.ID==0 to decide Insert vs Update. Mirrors
// C# `IMetadataFileService.Upsert(IEnumerable)` semantics — accepts
// a mixed list of new + existing rows and writes each appropriately.
//
// Errors short-circuit the loop (matches C# `Upsert` which throws
// on the first failure).
// DeleteMany loops over Delete. metadatafiles.Repo has no bulk-delete
// helper; the Repository[T].Delete generic does the per-id work.
// Errors from individual Deletes (other than ErrNotFound) propagate
// after recording; missing IDs are silently swallowed.
func (a *metadataFileStoreAdapter) DeleteMany(ctx context.Context, ids []int) error {
	for _, id := range ids {
		err := a.repo.Delete(ctx, id)
		if err != nil && !errors.Is(err, metadatafiles.ErrNotFound) {
			return fmt.Errorf("metadataFileStoreAdapter: DeleteMany id=%d: %w", id, err)
		}
	}
	return nil
}

func (a *metadataFileStoreAdapter) Upsert(ctx context.Context, files []metadatafiles.MetadataFile) error {
	for i := range files {
		f := &files[i]
		if f.ID == 0 {
			if _, err := a.Insert(ctx, f); err != nil {
				return fmt.Errorf("metadataFileStoreAdapter: Upsert insert: %w", err)
			}
			continue
		}
		if err := a.repo.Update(ctx, f); err != nil {
			return fmt.Errorf("metadataFileStoreAdapter: Upsert update (id=%d): %w", f.ID, err)
		}
	}
	return nil
}

// metadataAttrsNoop is a no-op [metadataservice.MediaFileAttributes].
// The Go port's *disk.Provider doesn't expose a SetFilePermissions
// helper that matches the metadataservice contract today; this
// no-op preserves the orchestrator's call-site shape while a real
// post-write chmod helper is wired in a follow-up cleanup slice.
//
// TODO: replace with a real adapter once the disk.Provider exposes
// a single-arg per-file SetFilePermissions hook (or once a sibling
// internal/mediafileattributes package ports).
type metadataAttrsNoop struct{}

func (metadataAttrsNoop) SetFilePermissions(_ string) error { return nil }

// metadataRecycleBinAdapter wraps *recyclebin.Service. The
// recyclebin's DeleteFile signature is `(path, subfolder) (string,
// error)` — the metadataservice contract drops the returned new-path
// string since the orchestrator doesn't need it.
type metadataRecycleBinAdapter struct {
	svc *recyclebin.Service
}

func newMetadataRecycleBinAdapter(s *recyclebin.Service) *metadataRecycleBinAdapter {
	return &metadataRecycleBinAdapter{svc: s}
}

func (a *metadataRecycleBinAdapter) DeleteFile(path, subfolder string) error {
	if a == nil || a.svc == nil {
		return errors.New("metadataRecycleBinAdapter: nil svc")
	}
	_, err := a.svc.DeleteFile(path, subfolder)
	return err
}

// metadataOtherRenamerNoop is a no-op [metadataservice.OtherExtraRenamer].
// The Go port has no other-extras subsystem (subtitles + raw extras
// haven't ported); the C# tree's `IOtherExtraFileRenamer.RenameOtherExtraFile`
// call sites are preserved as no-ops until that sibling subsystem
// ports.
type metadataOtherRenamerNoop struct{}

func (metadataOtherRenamerNoop) RenameOtherExtraFile(_ *series.Series, _ string) error {
	return nil
}

// metadataTransferAdapter wraps *disk.Provider's MoveFile to satisfy
// [metadataservice.DiskTransfer]. The C# tree has a separate
// `IDiskTransferService.TransferFile` that handles cross-filesystem
// safely; the Go port's MoveFile is enough for the metadata-sidecar
// case (per-series-folder moves are always same-filesystem).
type metadataTransferAdapter struct {
	provider *disk.Provider
}

func newMetadataTransferAdapter(p *disk.Provider) *metadataTransferAdapter {
	return &metadataTransferAdapter{provider: p}
}

func (a *metadataTransferAdapter) TransferFile(src, dst string, _ metadataservice.TransferModeT) error {
	if a == nil || a.provider == nil {
		return errors.New("metadataTransferAdapter: nil provider")
	}
	// Both TransferModeMove and TransferModeCopy collapse to MoveFile
	// here — MetadataService only ever passes Move (see C# `:310,
	// :441` call sites). Documented if a future caller needs Copy.
	return a.provider.MoveFile(src, dst)
}

// metadataSeasonsAdapter decodes series.Series.Seasons JSON-blob
// into []metadata.Season. Mirrors the C# `series.Seasons` accessor.
//
// Stateless after construction; goroutine-safe.
type metadataSeasonsAdapter struct{}

func (metadataSeasonsAdapter) SeasonsForSeries(s *series.Series) ([]metadata.Season, error) {
	if s == nil {
		return nil, nil
	}
	body := s.Seasons
	if body == "" || body == "[]" {
		return nil, nil
	}
	var out []metadata.Season
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("metadataSeasonsAdapter: decode Seasons JSON: %w", err)
	}
	return out, nil
}

// metadataWriterFactoryAdapter walks the package-global
// [metadata.Default] registry + the persisted Metadata.Enable column
// to produce the Enabled / All views the
// [metadataservice.WriterFactory] consumes.
//
// The C# tree's `MetadataFactory.Enabled()` returns each
// IMetadata-instance-constructed-from-each-row-where-Enable=true.
// The Go port follows the same shape — `Repo.All` returns every
// persisted Metadata; we filter by Enable for Enabled() and skip
// the filter for All().
//
// Per-row Constructor invocation passes [metadata.Dependencies]
// populated from the adapter's bundled deps (disk + mediacover
// + tags etc. — wired by main.go at startup).
type metadataWriterFactoryAdapter struct {
	repo *metadata.Repo
	deps metadata.Dependencies
	reg  *metadata.Registry
}

func newMetadataWriterFactoryAdapter(repo *metadata.Repo, deps metadata.Dependencies) *metadataWriterFactoryAdapter {
	return &metadataWriterFactoryAdapter{repo: repo, deps: deps, reg: metadata.Default()}
}

func (a *metadataWriterFactoryAdapter) Enabled(ctx context.Context) ([]metadata.Provider, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataWriterFactoryAdapter: Enabled: All: %w", err)
	}
	var out []metadata.Provider
	for i := range rows {
		row := &rows[i]
		if !row.Enable {
			continue
		}
		provider, constructErr := a.reg.Construct(row, a.deps)
		if constructErr != nil {
			// Skip rows whose Constructor errors — surfaces as a
			// missing writer in the orchestrator's fan-out, which is
			// the same outcome as the C# tree where a misconfigured
			// IMetadata instance throws at GetInstance.
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}

func (a *metadataWriterFactoryAdapter) All(ctx context.Context) ([]metadata.Provider, error) {
	rows, err := a.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("metadataWriterFactoryAdapter: All: %w", err)
	}
	var out []metadata.Provider
	for i := range rows {
		row := &rows[i]
		provider, constructErr := a.reg.Construct(row, a.deps)
		if constructErr != nil {
			continue
		}
		out = append(out, provider)
	}
	return out, nil
}

// Compile-time guards pin every adapter to its target interface.
var (
	_ metadataservice.DiskWriter          = (*metadataDiskAdapter)(nil)
	_ metadataservice.HTTPDownloader      = (*metadataHTTPAdapter)(nil)
	_ metadataservice.MetadataFileStore   = (*metadataFileStoreAdapter)(nil)
	_ metadataservice.MediaFileAttributes = metadataAttrsNoop{}
	_ metadataservice.RecycleBin          = (*metadataRecycleBinAdapter)(nil)
	_ metadataservice.OtherExtraRenamer   = metadataOtherRenamerNoop{}
	_ metadataservice.DiskTransfer        = (*metadataTransferAdapter)(nil)
	_ metadataservice.SeasonsLister       = metadataSeasonsAdapter{}
	_ metadataservice.WriterFactory       = (*metadataWriterFactoryAdapter)(nil)
)
