package nzbget_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
	"sonarr.tv/sonarr/internal/ospath"
)

// ============================================================================
// Slice 6.B7: NZBGet RemotePathMapper wire-up — historyItemToItem +
// GetStatus consult the mapper to translate the NZBGet-side absolute
// paths (FinalDir / DestDir / per-category DestDir) back to Sonarr-
// side paths when the off-host NZBGet instance writes to a directory
// that isn't mounted at the same path on the Sonarr host.
// ============================================================================

// stubMapper satisfies downloadclients.RemotePathMapper with a fixed
// host-keyed prefix-swap rule.
type stubMapper struct {
	host         string
	remotePrefix string
	localPrefix  string
	err          error
}

func (m *stubMapper) RemapRemoteToLocal(_ context.Context, host string, remotePath ospath.OsPath) (ospath.OsPath, error) {
	if m.err != nil {
		return remotePath, m.err
	}
	if !strings.EqualFold(host, m.host) {
		return remotePath, nil
	}
	raw := remotePath.FullPath()
	if !strings.HasPrefix(raw, m.remotePrefix) {
		return remotePath, nil
	}
	return ospath.New(m.localPrefix + strings.TrimPrefix(raw, m.remotePrefix)), nil
}

func TestNzbget_GetItems_RemapsHistoryOutputPath(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.historyBody = `[{"NZBID":42,"NZBName":"Show.S01E01","Name":"Show.S01E01","Category":"tv","FinalDir":"/nzbget/done/Show.S01E01","DestDir":"/nzbget/intermediate","FileSizeLo":1024,"FileSizeHi":0,"Parameters":[],"ParStatus":"SUCCESS","UnpackStatus":"NONE","MoveStatus":"NONE","ScriptStatus":"NONE","DeleteStatus":"NONE","MarkStatus":"NONE"}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) { s.TvCategory = "tv" })
	n.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/nzbget/done",
		localPrefix:  "/sonarr/done",
	})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sonarr/done/Show.S01E01"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want %q (FinalDir → remapped)", got[0].OutputPath.FullPath(), want)
	}
}

func TestNzbget_GetItems_NilMapperKeepsRawNzbgetPath(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.historyBody = `[{"NZBID":42,"NZBName":"X","Name":"X","Category":"tv","FinalDir":"/nzbget/done/X","DestDir":"","FileSizeLo":1,"FileSizeHi":0,"Parameters":[],"ParStatus":"SUCCESS","UnpackStatus":"NONE","MoveStatus":"NONE","ScriptStatus":"NONE","DeleteStatus":"NONE","MarkStatus":"NONE"}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) { s.TvCategory = "tv" })
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/nzbget/done/X"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q (nil mapper)", got[0].OutputPath.FullPath(), want)
	}
}

func TestNzbget_GetStatus_RemapsCategoryDestDir(t *testing.T) {
	t.Parallel()
	_, srv := newStatusServer(t, []nzbget.NzbgetConfigItem{
		{Name: "Category1.Name", Value: "tv"},
		{Name: "Category1.DestDir", Value: "/nzbget/downloads/tv"},
	})
	n := newNzbgetForStatus(t, srv, func(s *nzbget.Settings) { s.TvCategory = "tv" })
	n.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/nzbget/downloads",
		localPrefix:  "/sonarr/downloads",
	})
	info, err := n.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %+v, want 1 entry", info.OutputRootFolders)
	}
	if want := "/sonarr/downloads/tv"; info.OutputRootFolders[0].FullPath() != want {
		t.Errorf("OutputRootFolders[0] = %q, want %q", info.OutputRootFolders[0].FullPath(), want)
	}
}

func TestNzbget_RemapErrorFallsBackToRawPath(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.historyBody = `[{"NZBID":42,"NZBName":"X","Name":"X","Category":"tv","FinalDir":"/nzbget/x","DestDir":"","FileSizeLo":1,"FileSizeHi":0,"Parameters":[],"ParStatus":"SUCCESS","UnpackStatus":"NONE","MoveStatus":"NONE","ScriptStatus":"NONE","DeleteStatus":"NONE","MarkStatus":"NONE"}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) { s.TvCategory = "tv" })
	n.SetRemotePathMapper(&stubMapper{host: settingsFor(t, srv).Host, err: errors.New("rpm blew up")})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/nzbget/x"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q on remap error", got[0].OutputPath.FullPath(), want)
	}
}

// Compile-time guard.
var _ downloadclients.RemotePathMapper = (*stubMapper)(nil)
