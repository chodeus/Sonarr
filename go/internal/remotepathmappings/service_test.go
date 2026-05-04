package remotepathmappings_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/ospath"
	rpm "sonarr.tv/sonarr/internal/remotepathmappings"
)

// stubDisk implements remotepathmappings.DiskProvider with a single
// configurable boolean. Defaults to "exists=true" — matches the C#
// fixture's `.Setup(m => m.FolderExists(...)).Returns(true)` Setup
// at `RemotePathMappingServiceFixture.cs:20-22`.
type stubDisk struct{ exists bool }

func (s *stubDisk) FolderExists(string) bool { return s.exists }

func newServiceSQLite(t *testing.T) (*rpm.Service, *rpm.Repo, *stubDisk) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := rpm.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	disk := &stubDisk{exists: true}
	svc, err := rpm.NewService(repo, disk)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, repo, disk
}

// seedMapping inserts the canonical fixture mapping
// (`my-server.localdomain`, `/mnt/storage/`, `/mnt/mountedstorage/`)
// — the C# `GivenMapping()` helper at
// `RemotePathMappingServiceFixture.cs:33-45` (with the OS-agnostic
// `D:\mountedstorage\` rendered to its Unix form, since the Go test
// runs on a Unix host).
func seedMapping(t *testing.T, svc *rpm.Service) {
	t.Helper()
	m := &rpm.Mapping{
		Host:       "my-server.localdomain",
		RemotePath: "/mnt/storage/",
		LocalPath:  "/mnt/mountedstorage/",
	}
	if _, err := svc.Add(t.Context(), m); err != nil {
		t.Fatalf("seedMapping: %v", err)
	}
}

// Ports `should_be_able_to_add_new_mapping` (2 [TestCase] rows). The
// C# version uses Mocker.Verify(c => c.Insert(mapping), Times.Once)
// — the Go port verifies via the persisted Get round-trip instead.
func TestService_Add_AcceptsNewMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host, remote, local string
	}{
		{"my-first-server.localdomain", "/mnt/storage", "/mnt/storage1"},
		{"my-server.localdomain", "/mnt/storage2", "/mnt/storage2"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)
			seedMapping(t, svc)

			m := &rpm.Mapping{Host: tc.host, RemotePath: tc.remote, LocalPath: tc.local}
			if _, err := svc.Add(t.Context(), m); err != nil {
				t.Fatalf("Add: %v", err)
			}
			if m.ID == 0 {
				t.Errorf("expected ID populated post-Add")
			}
		})
	}
}

// Ports `should_be_able_to_remove_mapping`.
func TestService_Remove(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newServiceSQLite(t)
	seedMapping(t, svc)

	all, _ := svc.All(t.Context())
	if len(all) != 1 {
		t.Fatalf("seed: len=%d want 1", len(all))
	}
	if err := svc.Remove(t.Context(), all[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := repo.Get(t.Context(), all[0].ID); !errors.Is(err, rpm.ErrNotFound) {
		t.Errorf("post-Remove Get: %v want ErrNotFound", err)
	}
}

// Ports `adding_duplicated_mapping_should_throw` (2 [TestCase] rows).
// The C# code throws InvalidOperationException; the Go port returns
// [ErrDuplicateMapping].
func TestService_Add_DuplicateMappingFails(t *testing.T) {
	t.Parallel()
	cases := []struct{ host, remote, local string }{
		{"my-server.localdomain", "/mnt/storage", "/mnt/mountedstorage"},
		{"my-server.localdomain", "/mnt/storage", "/mnt/mountedstorage2"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.local, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)
			seedMapping(t, svc)

			m := &rpm.Mapping{Host: tc.host, RemotePath: tc.remote, LocalPath: tc.local}
			_, err := svc.Add(t.Context(), m)
			if !errors.Is(err, rpm.ErrDuplicateMapping) {
				t.Errorf("Add(dup): got %v want ErrDuplicateMapping", err)
			}
		})
	}
}

// Ports `should_remap_remote_to_local` (4 [TestCase] rows). The
// `D:\mountedstorage` LocalPath of the fixture is rendered as
// `/mnt/mountedstorage` for the Unix-host run; the case-insensitive
// host match is exercised by the second case (uppercased host).
func TestService_RemapRemoteToLocal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host, remote, expectedLocal string
	}{
		{"my-server.localdomain", "/mnt/storage/downloads/tv", "/mnt/mountedstorage/downloads/tv"},
		{"My-Server.localdomain", "/mnt/storage/downloads/tv", "/mnt/mountedstorage/downloads/tv"},
		// Different host → unchanged.
		{"my-2server.localdomain", "/mnt/storage/downloads/tv", "/mnt/storage/downloads/tv"},
		// Same host, non-matching prefix → unchanged.
		{"my-server.localdomain", "/mnt/storageabc/downloads/tv", "/mnt/storageabc/downloads/tv"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.host+"|"+tc.remote, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)
			seedMapping(t, svc)

			result, err := svc.RemapRemoteToLocal(t.Context(), tc.host, ospath.New(tc.remote))
			if err != nil {
				t.Fatalf("RemapRemoteToLocal: %v", err)
			}
			if !result.Equals(ospath.New(tc.expectedLocal)) {
				t.Errorf("RemapRemoteToLocal(%q,%q): got %q want %q", tc.host, tc.remote, result, tc.expectedLocal)
			}
		})
	}
}

// Ports `should_remap_local_to_remote` (5 [TestCase] rows).
func TestService_RemapLocalToRemote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		host, expectedRemote, local string
	}{
		{"my-server.localdomain", "/mnt/storage/downloads/tv", "/mnt/mountedstorage/downloads/tv"},
		{"My-Server.localdomain", "/mnt/storage/downloads/tv", "/mnt/mountedstorage/downloads/tv"},
		// Local equals the mapping's local prefix exactly.
		{"my-server.localdomain", "/mnt/storage/", "/mnt/mountedstorage"},
		// Different host → unchanged.
		{"my-2server.localdomain", "/mnt/storage/downloads/tv", "/mnt/storage/downloads/tv"},
		// Same host, non-matching prefix → unchanged.
		{"my-server.localdomain", "/mnt/storageabc/downloads/tv", "/mnt/storageabc/downloads/tv"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.host+"|"+tc.local, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)
			seedMapping(t, svc)

			result, err := svc.RemapLocalToRemote(t.Context(), tc.host, ospath.New(tc.local))
			if err != nil {
				t.Fatalf("RemapLocalToRemote: %v", err)
			}
			if !result.Equals(ospath.New(tc.expectedRemote)) {
				t.Errorf("RemapLocalToRemote(%q,%q): got %q want %q", tc.host, tc.local, result, tc.expectedRemote)
			}
		})
	}
}

// Ports `should_fix_wrong_slashes_on_add` (3 [TestCase] rows). The
// path-normalization runs before validation, so even Windows-shaped
// paths on a Unix host get cleaned up via OsPath.AsDirectory().
func TestService_Add_FixesWrongSlashes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input, expected string
	}{
		{`\\server\share\with/mixed/slashes`, `\\server\share\with\mixed\slashes\`},
		{`D:/with/forward/slashes`, `D:\with\forward\slashes\`},
		{`D:/with/mixed\slashes`, `D:\with\mixed\slashes\`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)

			m := &rpm.Mapping{
				Host:       "my-server.localdomain",
				RemotePath: tc.input,
				LocalPath:  "/mnt/mountedstorage/downloads/tv",
			}
			result, err := svc.Add(t.Context(), m)
			if err != nil {
				t.Fatalf("Add(%q): %v", tc.input, err)
			}
			if result.RemotePath != tc.expected {
				t.Errorf("post-Add RemotePath: got %q want %q", result.RemotePath, tc.expected)
			}
		})
	}
}

// Extra coverage beyond the C# fixture: the validate block.
func TestService_Add_Validation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    rpm.Mapping
		want error
	}{
		{
			name: "empty host",
			m:    rpm.Mapping{Host: "", RemotePath: "/r", LocalPath: "/mnt/x"},
			want: rpm.ErrInvalidHost,
		},
		{
			name: "whitespace host",
			m:    rpm.Mapping{Host: "   ", RemotePath: "/r", LocalPath: "/mnt/x"},
			want: rpm.ErrInvalidHost,
		},
		{
			name: "remote leading space",
			m:    rpm.Mapping{Host: "h", RemotePath: " /r", LocalPath: "/mnt/x"},
			want: rpm.ErrInvalidRemotePath,
		},
		{
			name: "empty remote",
			m:    rpm.Mapping{Host: "h", RemotePath: "", LocalPath: "/mnt/x"},
			want: rpm.ErrInvalidRemotePath,
		},
		{
			name: "empty local",
			m:    rpm.Mapping{Host: "h", RemotePath: "/r", LocalPath: ""},
			want: rpm.ErrInvalidLocalPath,
		},
		{
			name: "unrooted local",
			m:    rpm.Mapping{Host: "h", RemotePath: "/r", LocalPath: "relative/path"},
			want: rpm.ErrInvalidLocalPath,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newServiceSQLite(t)
			row := tc.m
			_, err := svc.Add(t.Context(), &row)
			if !errors.Is(err, tc.want) {
				t.Errorf("Add: got %v want %v", err, tc.want)
			}
		})
	}
}

// Extra coverage: missing-folder rejection. Disk stub returns false.
func TestService_Add_RejectsMissingFolder(t *testing.T) {
	t.Parallel()
	svc, _, disk := newServiceSQLite(t)
	disk.exists = false
	m := &rpm.Mapping{Host: "h", RemotePath: "/r", LocalPath: "/mnt/x"}
	_, err := svc.Add(t.Context(), &rpm.Mapping{Host: m.Host, RemotePath: m.RemotePath, LocalPath: m.LocalPath})
	if !errors.Is(err, rpm.ErrLocalFolderNotFound) {
		t.Errorf("Add: got %v want ErrLocalFolderNotFound", err)
	}
}

// Extra coverage: empty path passthrough. Mirrors C# `:130-133` and
// `:161-164` early-return short-circuits.
func TestService_Remap_EmptyPathPassesThrough(t *testing.T) {
	t.Parallel()
	svc, _, _ := newServiceSQLite(t)
	seedMapping(t, svc)

	got, err := svc.RemapRemoteToLocal(t.Context(), "my-server.localdomain", ospath.Null())
	if err != nil {
		t.Fatalf("RemapRemoteToLocal: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty passthrough, got %q", got)
	}
	got, err = svc.RemapLocalToRemote(t.Context(), "my-server.localdomain", ospath.Null())
	if err != nil {
		t.Fatalf("RemapLocalToRemote: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty passthrough, got %q", got)
	}
}

// Extra coverage: Update validation excludes the row being edited so
// re-saving an unchanged mapping doesn't trip the duplicate check.
func TestService_Update_ExcludesSelfFromDuplicateCheck(t *testing.T) {
	t.Parallel()
	svc, _, _ := newServiceSQLite(t)
	seedMapping(t, svc)
	all, _ := svc.All(t.Context())
	row := all[0]
	// Re-save with no changes — should succeed.
	if _, err := svc.Update(t.Context(), &row); err != nil {
		t.Errorf("Update(self): %v", err)
	}
	// Re-save with a tweak — should also succeed.
	row.LocalPath = "/mnt/elsewhere/"
	if _, err := svc.Update(t.Context(), &row); err != nil {
		t.Errorf("Update(tweak): %v", err)
	}
}

// Extra coverage: cache populated on first All() call, mutations
// invalidate it.
func TestService_All_CachesAndInvalidates(t *testing.T) {
	t.Parallel()
	svc, repo, _ := newServiceSQLite(t)
	seedMapping(t, svc)

	all1, _ := svc.All(t.Context())
	if len(all1) != 1 {
		t.Fatalf("post-seed: len=%d", len(all1))
	}
	// Insert directly into the repo, bypassing the service. The
	// service's cache is now stale.
	if _, err := repo.Insert(t.Context(), &rpm.Mapping{Host: "extra", RemotePath: "/r2", LocalPath: "/l2"}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	all2, _ := svc.All(t.Context())
	if len(all2) != 1 {
		t.Errorf("expected stale cache hit (len=1), got %d — direct repo writes shouldn't invalidate", len(all2))
	}
	// A mutation through the service invalidates the cache.
	if err := svc.Remove(t.Context(), all1[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	all3, _ := svc.All(t.Context())
	if len(all3) != 1 {
		t.Errorf("post-mutation reload: len=%d want 1 (the row inserted via repo)", len(all3))
	}
}
