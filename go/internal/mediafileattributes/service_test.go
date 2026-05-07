package mediafileattributes_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/episodefilemoving"
	"sonarr.tv/sonarr/internal/mediafileattributes"
)

type stubConfig struct {
	enabled bool
	mask    string
	group   string
}

func (s stubConfig) SetPermissionsLinux() bool { return s.enabled }
func (s stubConfig) ChmodFolder() string       { return s.mask }
func (s stubConfig) ChownGroup() string        { return s.group }

type stubDisk struct {
	calls []setPermissionsCall
	err   error
}

type setPermissionsCall struct {
	path, mask, group string
}

func (s *stubDisk) SetPermissions(path, mask, group string) error {
	s.calls = append(s.calls, setPermissionsCall{path, mask, group})
	return s.err
}

func TestNewService_NilConfig(t *testing.T) {
	t.Parallel()
	if got := mediafileattributes.NewService(nil, &stubDisk{}, nil); got != nil {
		t.Errorf("NewService(nil cfg) = %v, want nil", got)
	}
}

func TestNewService_NilDisk(t *testing.T) {
	t.Parallel()
	if got := mediafileattributes.NewService(stubConfig{}, nil, nil); got != nil {
		t.Errorf("NewService(nil disk) = %v, want nil", got)
	}
}

func TestNewService_OK(t *testing.T) {
	t.Parallel()
	if got := mediafileattributes.NewService(stubConfig{}, &stubDisk{}, slog.Default()); got == nil {
		t.Fatal("NewService(non-nil deps) = nil, want non-nil")
	}
}

func TestSetFilePermissions_DisabledIsNoop(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	svc := mediafileattributes.NewService(stubConfig{enabled: false}, d, slog.Default())
	if err := svc.SetFilePermissions("/x/y.mkv"); err != nil {
		t.Fatalf("SetFilePermissions: %v", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("disk.SetPermissions called %d times, want 0 (disabled)", len(d.calls))
	}
}

func TestSetFilePermissions_PassesMaskAndGroup(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	cfg := stubConfig{enabled: true, mask: "775", group: "media"}
	svc := mediafileattributes.NewService(cfg, d, slog.Default())
	if err := svc.SetFilePermissions("/x/y.mkv"); err != nil {
		t.Fatalf("SetFilePermissions: %v", err)
	}
	if len(d.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(d.calls))
	}
	got := d.calls[0]
	if got.path != "/x/y.mkv" || got.mask != "775" || got.group != "media" {
		t.Errorf("call = %+v, want path=/x/y.mkv mask=775 group=media", got)
	}
}

func TestSetFolderPermissions_PassesMaskAndGroup(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	cfg := stubConfig{enabled: true, mask: "755", group: ""}
	svc := mediafileattributes.NewService(cfg, d, slog.Default())
	if err := svc.SetFolderPermissions("/x/dir"); err != nil {
		t.Fatalf("SetFolderPermissions: %v", err)
	}
	if len(d.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(d.calls))
	}
	got := d.calls[0]
	if got.path != "/x/dir" || got.mask != "755" || got.group != "" {
		t.Errorf("call = %+v", got)
	}
}

func TestSetFilePermissions_SwallowsDiskError(t *testing.T) {
	t.Parallel()
	d := &stubDisk{err: errors.New("boom")}
	cfg := stubConfig{enabled: true, mask: "755"}
	svc := mediafileattributes.NewService(cfg, d, slog.Default())
	// Mirrors C# `:90-94` — best-effort: error is logged + swallowed.
	if err := svc.SetFilePermissions("/x/y.mkv"); err != nil {
		t.Fatalf("SetFilePermissions returned %v, want nil (swallow)", err)
	}
}

func TestSetFilePermissions_SwallowsNotExist(t *testing.T) {
	t.Parallel()
	d := &stubDisk{err: fs.ErrNotExist}
	cfg := stubConfig{enabled: true, mask: "755"}
	svc := mediafileattributes.NewService(cfg, d, slog.Default())
	if err := svc.SetFilePermissions("/missing/y.mkv"); err != nil {
		t.Fatalf("SetFilePermissions(missing) returned %v, want nil", err)
	}
}

func TestSetFolderLastWriteTime_NoopOnNonWindows(t *testing.T) {
	t.Parallel()
	d := &stubDisk{}
	svc := mediafileattributes.NewService(stubConfig{enabled: true, mask: "755"}, d, slog.Default())
	// Mirrors C# `:69-75` — non-Windows arm is a complete no-op.
	if err := svc.SetFolderLastWriteTime("/x/dir", time.Now()); err != nil {
		t.Fatalf("SetFolderLastWriteTime returned %v, want nil", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("disk called %d times, want 0", len(d.calls))
	}
}

// Compile-time check: Service satisfies the writer's AttributeWriter
// seam. Without this, a future refactor to either the interface or
// the Service would fail at the production wire-in instead of at
// build time. Mirrors the convention in
// `episodefilemoving/transfer.go` (`var _ Filesystem = …`).
func TestService_SatisfiesAttributeWriter(t *testing.T) {
	t.Parallel()
	var _ episodefilemoving.AttributeWriter = (*mediafileattributes.Service)(nil)
}

// Compile-time check: *disk.Provider satisfies the local DiskOps
// seam. Lives in the test package so the production binary doesn't
// import disk just to assert.
func TestDiskProvider_SatisfiesDiskOps(t *testing.T) {
	t.Parallel()
	var _ mediafileattributes.DiskOps = (*disk.Provider)(nil)
}
