package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/ospath"
)

type fakeRootLister struct {
	paths []string
	err   error
}

func (f fakeRootLister) AllRootFolderPaths(_ context.Context) ([]string, error) {
	return f.paths, f.err
}

func TestDownloadClientRootFolderCheck_NewRejectsNils(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewDownloadClientRootFolderCheck(nil, fakeRootLister{}, nil); err == nil {
		t.Errorf("expected error for nil enumerator")
	}
	if _, err := healthcheck.NewDownloadClientRootFolderCheck(fakeDCStatusEnumerator{}, nil, nil); err == nil {
		t.Errorf("expected error for nil root lister")
	}
}

func TestDownloadClientRootFolderCheck_OKNoRoots(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{},
		fakeRootLister{},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_OKNoClients(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_OKDisjoint(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "qBit", Status: &downloadclients.ClientInfo{
			OutputRootFolders: []ospath.OsPath{ospath.New("/downloads/torrents")},
		}},
	}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK (no overlap)", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_WarnsOnExactMatch(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "qBit", Status: &downloadclients.ClientInfo{
			OutputRootFolders: []ospath.OsPath{ospath.New("/mnt/tv")},
		}},
	}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonDownloadClientRootFolder {
		t.Errorf("reason = %v", got.Reason)
	}
	if !strings.Contains(got.Message, "qBit") {
		t.Errorf("message missing client name: %q", got.Message)
	}
	if !strings.Contains(got.Message, "/mnt/tv") {
		t.Errorf("message missing root path: %q", got.Message)
	}
}

func TestDownloadClientRootFolderCheck_WarnsWhenRootIsParent(t *testing.T) {
	t.Parallel()
	// DC output `/mnt/tv/downloads` lives INSIDE root `/mnt/tv`.
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Sab", Status: &downloadclients.ClientInfo{
			OutputRootFolders: []ospath.OsPath{ospath.New("/mnt/tv/downloads")},
		}},
	}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning (root is parent of output)", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_SkipsClientsWithErr(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "Broken", Err: errors.New("construct failed")},
		{Name: "qBit", Status: &downloadclients.ClientInfo{
			OutputRootFolders: []ospath.OsPath{ospath.New("/mnt/tv")},
		}},
	}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning (broken skipped, qBit still flagged)", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_NilStatusIgnored(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{{Name: "Mysterious", Status: nil}}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_EmptyOutputPathIgnored(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "qBit", Status: &downloadclients.ClientInfo{
			OutputRootFolders: []ospath.OsPath{ospath.New("")},
		}},
	}
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{rows: rows},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK (empty path skipped)", got.Type)
	}
}

func TestDownloadClientRootFolderCheck_RootListerError(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{},
		fakeRootLister{err: errors.New("root boom")},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "root boom") {
		t.Errorf("message: %q", got.Message)
	}
}

func TestDownloadClientRootFolderCheck_ClientListerError(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewDownloadClientRootFolderCheck(
		fakeDCStatusEnumerator{err: errors.New("client boom")},
		fakeRootLister{paths: []string{"/mnt/tv"}},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning", got.Type)
	}
	if !strings.Contains(got.Message, "client boom") {
		t.Errorf("message: %q", got.Message)
	}
}
