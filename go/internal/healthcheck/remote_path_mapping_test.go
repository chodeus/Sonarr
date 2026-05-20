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

type stubRPMConfig struct{ enabled bool }

func (s stubRPMConfig) EnableCompletedDownloadHandling() bool { return s.enabled }

type stubRPMOsInfo struct{ docker bool }

func (s stubRPMOsInfo) IsDocker() bool { return s.docker }

type stubRPMFolderExister struct {
	existing map[string]bool
}

func (s stubRPMFolderExister) FolderExists(path string) bool {
	return s.existing[path]
}

type stubRPMClients struct {
	out []healthcheck.EnumeratedDownloadClient
	err error
}

func (s stubRPMClients) EnabledDownloadClients(_ context.Context) ([]healthcheck.EnumeratedDownloadClient, error) {
	return s.out, s.err
}

// makeClient builds an EnumeratedDownloadClient with the
// supplied OutputRootFolders and IsLocalhost flag.
func makeClient(name string, isLocalhost bool, folders ...string) healthcheck.EnumeratedDownloadClient {
	roots := make([]ospath.OsPath, 0, len(folders))
	for _, f := range folders {
		roots = append(roots, ospath.New(f))
	}
	return healthcheck.EnumeratedDownloadClient{
		Name: name,
		Status: &downloadclients.ClientInfo{
			IsLocalhost:       isLocalhost,
			OutputRootFolders: roots,
		},
	}
}

func TestRPMC_PassesWhenCDHDisabled(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: false},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("bad", false, "C:\\bad\\windows\\path"),
		}},
		stubRPMFolderExister{},
		stubRPMOsInfo{},
		"linux",
		nil,
	)
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("CDH disabled: got %v, want OK (early-exit)", got.Type)
	}
}

func TestRPMC_PassesWhenNoClients(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: nil},
		stubRPMFolderExister{},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("no clients: got %v, want OK", got.Type)
	}
}

func TestRPMC_PassesWhenAllFoldersValid(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("ok", true, "/data/downloads"),
		}},
		stubRPMFolderExister{existing: map[string]bool{"/data/downloads": true}},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("valid local folder: got %v, want OK", got.Type)
	}
}

func TestRPMC_RemoteWrongOSPath(t *testing.T) {
	t.Parallel()
	// Windows-shaped path on a Linux host + remote client → WrongOSPath.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("remote-qb", false, "C:\\downloads"),
		}},
		stubRPMFolderExister{},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingWrongOSPath {
		t.Errorf("Reason: %v, want WrongOSPath", got.Reason)
	}
	if !strings.Contains(got.Message, "remote-qb") || !strings.Contains(got.Message, "C:\\downloads") {
		t.Errorf("Message: %q (expected client + path)", got.Message)
	}
}

func TestRPMC_DockerBadPath(t *testing.T) {
	t.Parallel()
	// Windows-shaped path on Linux+Docker localhost → BadDockerPath.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("local-dc", true, "C:\\downloads"),
		}},
		stubRPMFolderExister{},
		stubRPMOsInfo{docker: true},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingBadDockerPath {
		t.Errorf("Reason: %v, want BadDockerPath", got.Reason)
	}
	if !strings.Contains(got.Message, "Docker") {
		t.Errorf("Docker message missing: %q", got.Message)
	}
}

func TestRPMC_LocalWrongOSPath(t *testing.T) {
	t.Parallel()
	// Windows-shaped path on Linux localhost (non-docker) → LocalWrongOSPath.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("local-dc", true, "C:\\downloads"),
		}},
		stubRPMFolderExister{},
		stubRPMOsInfo{docker: false},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingLocalWrongOSPath {
		t.Errorf("Reason: %v, want LocalWrongOSPath", got.Reason)
	}
}

func TestRPMC_DockerFolderMissing(t *testing.T) {
	t.Parallel()
	// Valid path, doesn't exist on disk, docker → DockerFolderMissing.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("docker-dc", true, "/data/downloads"),
		}},
		stubRPMFolderExister{existing: map[string]bool{}}, // path NOT in existing
		stubRPMOsInfo{docker: true},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingDockerFolderMissing {
		t.Errorf("Reason: %v, want DockerFolderMissing", got.Reason)
	}
}

func TestRPMC_RemoteLocalFolderMissing(t *testing.T) {
	t.Parallel()
	// Valid path, doesn't exist on disk, non-localhost, non-docker → LocalFolderMissing.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("remote-dc", false, "/data/downloads"),
		}},
		stubRPMFolderExister{existing: map[string]bool{}},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingLocalFolderMissing {
		t.Errorf("Reason: %v, want LocalFolderMissing", got.Reason)
	}
}

func TestRPMC_LocalGenericPermissions(t *testing.T) {
	t.Parallel()
	// Valid path, doesn't exist on disk, localhost, non-docker → GenericPermissions.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("local-dc", true, "/data/downloads"),
		}},
		stubRPMFolderExister{existing: map[string]bool{}},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingGenericPermissions {
		t.Errorf("Reason: %v, want GenericPermissions", got.Reason)
	}
}

func TestRPMC_SkipsFailedClients(t *testing.T) {
	t.Parallel()
	// First client errored — should be skipped silently; the second
	// (with a missing folder) should still trip the check.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			{Name: "broken", Err: errors.New("unreachable")},
			makeClient("ok-but-missing", true, "/data/missing"),
		}},
		stubRPMFolderExister{existing: map[string]bool{}},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingGenericPermissions {
		t.Errorf("expected GenericPermissions from the second client; got %v", got.Reason)
	}
}

func TestRPMC_FirstFailureShortCircuits(t *testing.T) {
	t.Parallel()
	// Two clients both with problems — the first triggers WrongOSPath,
	// the second would trigger GenericPermissions. We get the first.
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{out: []healthcheck.EnumeratedDownloadClient{
			makeClient("first", false, "C:\\downloads"),
			makeClient("second", true, "/data/missing"),
		}},
		stubRPMFolderExister{existing: map[string]bool{}},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Reason != healthcheck.ReasonRemotePathMappingWrongOSPath {
		t.Errorf("Reason: %v, want WrongOSPath (first failure short-circuits)", got.Reason)
	}
}

func TestRPMC_ClientEnumerateErrorPasses(t *testing.T) {
	t.Parallel()
	c, _ := healthcheck.NewRemotePathMappingCheck(
		stubRPMConfig{enabled: true},
		stubRPMClients{err: errors.New("DB unreachable")},
		stubRPMFolderExister{},
		stubRPMOsInfo{},
		"linux", nil,
	)
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("client-enumerate failure: %v, want silent OK", got.Type)
	}
}

func TestNewRemotePathMappingCheck_RequiredArgs(t *testing.T) {
	t.Parallel()
	good := func() (healthcheck.RemotePathMappingCheckConfig,
		healthcheck.DownloadClientStatusEnumerator,
		healthcheck.RemotePathMappingFolderExister,
		healthcheck.RemotePathMappingCheckOsInfo) {
		return stubRPMConfig{}, stubRPMClients{}, stubRPMFolderExister{}, stubRPMOsInfo{}
	}
	cfg, clients, fe, osi := good()
	if _, err := healthcheck.NewRemotePathMappingCheck(nil, clients, fe, osi, "x", nil); err == nil {
		t.Errorf("nil cfg: want error")
	}
	if _, err := healthcheck.NewRemotePathMappingCheck(cfg, nil, fe, osi, "x", nil); err == nil {
		t.Errorf("nil clients: want error")
	}
	if _, err := healthcheck.NewRemotePathMappingCheck(cfg, clients, nil, osi, "x", nil); err == nil {
		t.Errorf("nil folderFn: want error")
	}
	if _, err := healthcheck.NewRemotePathMappingCheck(cfg, clients, fe, nil, "x", nil); err == nil {
		t.Errorf("nil osInfo: want error")
	}
}
