package deluge_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
	"sonarr.tv/sonarr/internal/ospath"
)

// ============================================================================
// Slice 6.B6: Deluge RemotePathMapper wire-up — torrentToItem +
// GetStatus consult the mapper to translate the deluge-side absolute
// paths back to Sonarr-side paths when the off-host deluge instance
// writes to a directory that isn't mounted at the same path on the
// Sonarr host.
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

func TestDeluge_GetItems_RemapsTorrentOutputPath(t *testing.T) {
	t.Parallel()
	body := `{"abc":{"hash":"abc","name":"Show.S01E01","state":"Downloading","progress":50.0,"eta":120,"message":"","is_finished":false,"save_path":"/deluge/downloads","total_size":1000,"total_done":500,"time_added":1700000000,"active_time":100,"ratio":0.0,"is_auto_managed":true,"stop_at_ratio":false,"remove_at_ratio":false,"stop_ratio":2.0}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	d.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/deluge/downloads",
		localPrefix:  "/sonarr/downloads",
	})

	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sonarr/downloads/Show.S01E01"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want %q", got[0].OutputPath.FullPath(), want)
	}
}

func TestDeluge_GetItems_NilMapperKeepsRawDelugePath(t *testing.T) {
	t.Parallel()
	body := `{"abc":{"hash":"abc","name":"Show.S01E01","state":"Downloading","progress":50.0,"eta":120,"message":"","is_finished":false,"save_path":"/deluge/downloads","total_size":1000,"total_done":500,"time_added":1700000000,"active_time":100,"ratio":0.0,"is_auto_managed":true,"stop_at_ratio":false,"remove_at_ratio":false,"stop_ratio":2.0}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)

	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/deluge/downloads/Show.S01E01"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q (nil mapper)", got[0].OutputPath.FullPath(), want)
	}
}

func TestDeluge_GetStatus_RemapsOutputRootFolders(t *testing.T) {
	t.Parallel()
	h, srv := newStatusServer(t)
	h.configBody = `{"move_completed":false,"download_location":"/deluge/downloads"}`
	d := newDelugeForStatus(t, srv, func(s *deluge.Settings) {
		// Leave CompletedDirectory + DownloadDirectory empty so the
		// resolver falls back to core.get_config's download_location.
	})
	d.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/deluge/downloads",
		localPrefix:  "/sonarr/downloads",
	})

	info, err := d.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if len(info.OutputRootFolders) != 1 {
		t.Fatalf("OutputRootFolders = %+v, want 1 entry", info.OutputRootFolders)
	}
	if want := "/sonarr/downloads"; info.OutputRootFolders[0].FullPath() != want {
		t.Errorf("OutputRootFolders[0] = %q, want %q", info.OutputRootFolders[0].FullPath(), want)
	}
}

func TestDeluge_RemapErrorFallsBackToRawPath(t *testing.T) {
	t.Parallel()
	body := `{"abc":{"hash":"abc","name":"X","state":"Downloading","progress":50.0,"eta":120,"message":"","is_finished":false,"save_path":"/deluge/x","total_size":1000,"total_done":500,"time_added":1700000000,"active_time":100,"ratio":0.0,"is_auto_managed":true,"stop_at_ratio":false,"remove_at_ratio":false,"stop_ratio":2.0}}`
	_, srv := newUpdateUIServer(t, body)
	d := newDelugeForGetItems(t, srv, nil)
	d.SetRemotePathMapper(&stubMapper{host: settingsFor(t, srv).Host, err: errors.New("rpm blew up")})

	got, err := d.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/deluge/x/X"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q on remap error", got[0].OutputPath.FullPath(), want)
	}
}

// Compile-time guard.
var _ downloadclients.RemotePathMapper = (*stubMapper)(nil)
