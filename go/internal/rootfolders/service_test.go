package rootfolders_test

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// stubDisk records calls and returns canned answers.
type stubDisk struct {
	mu sync.Mutex

	exists   bool
	writable bool
	empty    bool

	free  int64
	total int64

	freeErr  error
	totalErr error

	directoriesByPath map[string][]string
	getDirsErr        map[string]error

	calls int
}

func newStubDisk() *stubDisk {
	return &stubDisk{
		exists:            true,
		writable:          true,
		directoriesByPath: map[string][]string{},
		getDirsErr:        map[string]error{},
	}
}

func (s *stubDisk) FolderExists(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.exists
}
func (s *stubDisk) FolderWritable(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writable
}
func (s *stubDisk) FolderEmpty(string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.empty
}
func (s *stubDisk) GetAvailableSpace(string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.free, s.freeErr
}
func (s *stubDisk) GetTotalSize(string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.totalErr
}
func (s *stubDisk) GetDirectories(path string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.getDirsErr[path]; err != nil {
		return nil, err
	}
	return s.directoriesByPath[path], nil
}

type stubSeriesPaths struct{ paths map[int]string }

func (s *stubSeriesPaths) AllSeriesPaths(context.Context) (map[int]string, error) {
	if s.paths == nil {
		return map[int]string{}, nil
	}
	out := make(map[int]string, len(s.paths))
	for k, v := range s.paths {
		out[k] = v
	}
	return out, nil
}

type stubNaming struct{ format string }

func (s *stubNaming) GetSeriesFolderFormat(context.Context) (string, error) {
	return s.format, nil
}

type fixture struct {
	svc    *rootfolders.Service
	repo   *rootfolders.Repo
	disk   *stubDisk
	series *stubSeriesPaths
	naming *stubNaming
	db     *datastore.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	repo, err := rootfolders.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	disk := newStubDisk()
	series := &stubSeriesPaths{}
	naming := &stubNaming{format: "{Series Title}"}
	svc, err := rootfolders.NewService(repo, disk, series, naming)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &fixture{svc: svc, repo: repo, disk: disk, series: series, naming: naming, db: db}
}

// Ports `should_be_able_to_add_root_dir`. The C# fixture uses
// AsOsAgnostic(), which becomes a Unix path on this Mac host.
func TestService_Add_AcceptsRootedPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	got, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv-shows"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == 0 {
		t.Errorf("Add: expected ID populated")
	}
	if got.Path != "/mnt/tv-shows" {
		t.Errorf("Add: Path got %q", got.Path)
	}
}

// Ports `should_throw_if_folder_being_added_doesnt_exist`.
func TestService_Add_RejectsMissingFolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.disk.exists = false

	_, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv-shows"})
	if !errors.Is(err, rootfolders.ErrFolderNotFound) {
		t.Errorf("Add: got %v want ErrFolderNotFound", err)
	}
}

// Ports `should_be_able_to_remove_root_dir`.
func TestService_Remove(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	rf, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := f.svc.Remove(t.Context(), rf.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := f.repo.Get(t.Context(), rf.ID); !errors.Is(err, rootfolders.ErrNotFound) {
		t.Errorf("post-Remove Get: got %v want ErrNotFound", err)
	}
}

// Ports `invalid_folder_path_throws_on_add` (3 [TestCase] rows).
func TestService_Add_RejectsInvalidPath(t *testing.T) {
	t.Parallel()
	cases := []string{"", "BAD PATH", "relative/path"}
	for _, p := range cases {
		p := p
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			_, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: p})
			if !errors.Is(err, rootfolders.ErrInvalidPath) {
				t.Errorf("Add(%q): got %v want ErrInvalidPath", p, err)
			}
		})
	}
}

// Ports `adding_duplicated_root_folder_should_throw`. Duplicate detection
// is case-insensitive after `filepath.Clean`.
func TestService_Add_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		first, second string
	}{
		{"/mnt/tv", "/mnt/tv"},
		{"/mnt/tv", "/mnt/TV"},
		{"/mnt/tv", "/mnt/tv/"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.second, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if _, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: tc.first}); err != nil {
				t.Fatalf("Add(first): %v", err)
			}
			_, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: tc.second})
			if !errors.Is(err, rootfolders.ErrDuplicatePath) {
				t.Errorf("Add(dup %q): got %v want ErrDuplicatePath", tc.second, err)
			}
		})
	}
}

// Ports `should_throw_when_adding_not_writable_folder`.
func TestService_Add_RejectsNonWritableFolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.disk.writable = false

	_, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv"})
	if !errors.Is(err, rootfolders.ErrFolderNotWritable) {
		t.Errorf("Add: got %v want ErrFolderNotWritable", err)
	}
}

// Ports `should_get_root_folder_with_subfolders_excluding_special_sub_folders`
// (9 [TestCase] rows — one per special folder name).
func TestService_Get_ExcludesSpecialFolders(t *testing.T) {
	t.Parallel()
	cases := []string{
		"$recycle.bin", "system volume information", "recycler",
		"lost+found", ".appledb", ".appledesktop", ".appledouble",
		"@eadir", ".grab",
	}
	for _, sub := range cases {
		sub := sub
		t.Run(sub, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)

			rootPath := "/mnt/Test/TV"
			seeded, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: rootPath})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}

			subFolders := []string{"Series1", "Series2", "Series3", sub}
			abs := make([]string, 0, len(subFolders))
			for _, name := range subFolders {
				abs = append(abs, filepath.Join(rootPath, name))
			}
			f.disk.directoriesByPath[rootPath] = abs

			got, err := f.svc.Get(t.Context(), seeded.ID, true)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if len(got.UnmappedFolders) == 0 {
				t.Fatalf("expected unmapped folders, got 0")
			}
			for _, u := range got.UnmappedFolders {
				if u.Name == sub {
					t.Errorf("special folder %q leaked into unmapped list: %+v", sub, u)
				}
			}
		})
	}
}

// Ports `should_get_unmapped_folders_inside_letter_subfolder`. The
// SeriesFolderFormat has one separator → walk one extra level deep.
func TestService_Get_WalksDepthFromSeriesFolderFormat(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.naming.format = "{Series TitleFirstCharacter}" + string(filepath.Separator) + "{Series Title}"

	rootPath := "/mnt/Test/TV"
	letterFolder := filepath.Join(rootPath, "S")
	rf, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: rootPath})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.disk.directoriesByPath[rootPath] = []string{letterFolder}
	f.disk.directoriesByPath[letterFolder] = []string{
		filepath.Join(letterFolder, "Series1"),
		filepath.Join(letterFolder, "Series2"),
		filepath.Join(letterFolder, "Series3"),
	}

	got, err := f.svc.Get(t.Context(), rf.ID, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.UnmappedFolders) != 3 {
		t.Errorf("expected 3 unmapped folders, got %d: %+v", len(got.UnmappedFolders), got.UnmappedFolders)
	}
}

// Ports the GetBestRootFolderPathFixture cases.
func TestService_GetBestRootFolderPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		folders []string
		query   string
		want    string
	}{
		{
			name:    "parent path",
			folders: []string{"/mnt/Test/TV", "/mnt/Other/TV"},
			query:   "/mnt/Test/TV/Series Title",
			want:    "/mnt/Test/TV",
		},
		{
			name:    "grandparent path",
			folders: []string{"/mnt/Test/TV", "/mnt/Other/TV"},
			query:   "/mnt/Test/TV/S/Series Title",
			want:    "/mnt/Test/TV",
		},
		{
			name:    "no match falls back to parent dir",
			folders: []string{"/mnt/Test/TV", "/mnt/Other/TV"},
			query:   "/mnt/Different/TV/Series Title",
			want:    "/mnt/Different/TV",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			for _, p := range tc.folders {
				if _, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: p}); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			got, err := f.svc.GetBestRootFolderPath(t.Context(), tc.query)
			if err != nil {
				t.Fatalf("GetBestRootFolderPath: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// Cache-behaviour coverage: repeated lookups skip the repo, and Add/Remove
// invalidates the cache (so a freshly-added folder takes effect).
func TestService_GetBestRootFolderPath_CachedAndInvalidated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if _, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv"}); err != nil {
		t.Fatalf("Add(/mnt/tv): %v", err)
	}
	first, err := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/Show")
	if err != nil || first != "/mnt/tv" {
		t.Fatalf("first lookup: got %q err %v", first, err)
	}

	// Add a deeper folder; cache is invalidated → next lookup reflects it.
	if _, err := f.svc.Add(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv/anime"}); err != nil {
		t.Fatalf("Add(/mnt/tv/anime): %v", err)
	}
	got, err := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/anime/Show")
	if err != nil || got != "/mnt/tv/anime" {
		t.Errorf("post-Add lookup: got %q err %v want /mnt/tv/anime", got, err)
	}

	// Remove the deeper folder; cache invalidated again.
	all, _ := f.svc.All(t.Context())
	for _, r := range all {
		if r.Path == "/mnt/tv/anime" {
			if err := f.svc.Remove(t.Context(), r.ID); err != nil {
				t.Fatalf("Remove: %v", err)
			}
		}
	}
	post, err := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/anime/Show")
	if err != nil || post != "/mnt/tv" {
		t.Errorf("post-Remove lookup: got %q err %v want /mnt/tv", post, err)
	}
}

// Cache TTL coverage: with a tiny clock-controlled bound, an entry expires
// and is repopulated on the next call.
func TestService_GetBestRootFolderPath_TTLExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	f.svc.SetClock(func() time.Time { return now })

	if _, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got, _ := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/Show"); got != "/mnt/tv" {
		t.Errorf("first: %q", got)
	}
	// Insert a deeper folder via the repo (bypassing service-level invalidate).
	if _, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv/anime"}); err != nil {
		t.Fatalf("seed deeper: %v", err)
	}
	// Within TTL — cached value still served.
	if got, _ := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/Show"); got != "/mnt/tv" {
		t.Errorf("within TTL: got %q want /mnt/tv (cached)", got)
	}
	// Advance past 24h — cache miss reads the new state.
	now = now.Add(25 * time.Hour)
	f.svc.SetClock(func() time.Time { return now })
	if got, _ := f.svc.GetBestRootFolderPath(t.Context(), "/mnt/tv/anime/Show"); got != "/mnt/tv/anime" {
		t.Errorf("post-TTL: got %q want /mnt/tv/anime (refreshed)", got)
	}
}

// Async details: the timeout fires when the disk-stat work hangs longer
// than the bound. The Folder retains zero defaults.
func TestService_Get_TimeoutLeavesZeroDefaults(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.svc.SetDetailsTimeout(50 * time.Millisecond)

	// Wrap the disk stub so FolderEmpty blocks for longer than the bound.
	slow := &slowDisk{stub: f.disk, delay: 200 * time.Millisecond}
	repo, _ := rootfolders.NewRepo(f.db)
	svc, _ := rootfolders.NewService(repo, slow, f.series, f.naming)
	svc.SetDetailsTimeout(50 * time.Millisecond)

	rf, err := repo.Insert(t.Context(), &rootfolders.RootFolder{Path: "/mnt/tv"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := svc.Get(t.Context(), rf.ID, true)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Accessible || got.IsEmpty || got.FreeSpace != nil || got.TotalSpace != nil {
		t.Errorf("expected zero defaults after timeout, got %+v", got)
	}
}

// AllWithUnmappedFolders walks every persisted row.
func TestService_AllWithUnmappedFolders(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, p := range []string{"/mnt/tv", "/mnt/movies"} {
		if _, err := f.repo.Insert(t.Context(), &rootfolders.RootFolder{Path: p}); err != nil {
			t.Fatalf("seed %q: %v", p, err)
		}
	}
	f.disk.directoriesByPath["/mnt/tv"] = []string{"/mnt/tv/Show A", "/mnt/tv/Show B"}
	f.disk.directoriesByPath["/mnt/movies"] = []string{"/mnt/movies/Movie A"}
	f.series.paths = map[int]string{1: "/mnt/tv/Show A"}

	all, err := f.svc.AllWithUnmappedFolders(t.Context())
	if err != nil {
		t.Fatalf("AllWithUnmappedFolders: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 root folders, got %d", len(all))
	}

	// Sort for stable check.
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })

	if got := all[0].Path; got != "/mnt/movies" {
		t.Errorf("[0].Path=%q", got)
	}
	if names := folderNames(all[0].UnmappedFolders); len(names) != 1 || names[0] != "Movie A" {
		t.Errorf("[0].UnmappedFolders=%v", names)
	}

	if got := all[1].Path; got != "/mnt/tv" {
		t.Errorf("[1].Path=%q", got)
	}
	if names := folderNames(all[1].UnmappedFolders); len(names) != 1 || names[0] != "Show B" {
		t.Errorf("[1].UnmappedFolders=%v want [Show B]", names)
	}
}

func folderNames(in []rootfolders.UnmappedFolder) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		out = append(out, u.Name)
	}
	return out
}

// slowDisk wraps stubDisk and delays FolderExists/FolderEmpty so the
// async timeout path exercises the goroutine-still-running case.
type slowDisk struct {
	stub  *stubDisk
	delay time.Duration
}

func (d *slowDisk) FolderExists(p string) bool {
	time.Sleep(d.delay)
	return d.stub.FolderExists(p)
}
func (d *slowDisk) FolderWritable(p string) bool              { return d.stub.FolderWritable(p) }
func (d *slowDisk) FolderEmpty(p string) bool                 { return d.stub.FolderEmpty(p) }
func (d *slowDisk) GetAvailableSpace(p string) (int64, error) { return d.stub.GetAvailableSpace(p) }
func (d *slowDisk) GetTotalSize(p string) (int64, error)      { return d.stub.GetTotalSize(p) }
func (d *slowDisk) GetDirectories(p string) ([]string, error) { return d.stub.GetDirectories(p) }
