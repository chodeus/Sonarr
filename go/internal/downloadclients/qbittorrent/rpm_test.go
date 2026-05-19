package qbittorrent_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
	"sonarr.tv/sonarr/internal/ospath"
)

// ============================================================================
// Slice 6.B5: qBittorrent RemotePathMapper wire-up — torrentToItem +
// GetStatus consult the mapper to translate the WebUI-side
// ContentPath / SavePath back to Sonarr-side paths when the off-host
// qBittorrent instance writes to a directory that isn't mounted at the
// same path on the Sonarr host.
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

func TestQBittorrent_GetItems_RemapsContentPathForCompletedTorrent(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"abc","name":"Show.S01E01","size":1000,"progress":1.0,"state":"pausedUP","category":"tv","save_path":"/qb/downloads","content_path":"/qb/downloads/Show.S01E01","ratio":2.0,"ratio_limit":1.0,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })
	q.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/qb/downloads",
		localPrefix:  "/sonarr/downloads",
	})

	got, err := q.GetItems(context.Background())
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

func TestQBittorrent_GetItems_NilMapperKeepsRawWebUIPath(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"abc","name":"Show.S01E01","size":1000,"progress":1.0,"state":"pausedUP","category":"tv","save_path":"/qb/downloads","content_path":"/qb/downloads/Show.S01E01","ratio":2.0,"ratio_limit":1.0,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })
	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/qb/downloads/Show.S01E01"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q (nil mapper)", got[0].OutputPath.FullPath(), want)
	}
}

func TestQBittorrent_GetItems_RemapsSavePathJoinForDownloadingTorrent(t *testing.T) {
	t.Parallel()
	// Downloading state (state="downloading") → OutputPath built from
	// joinTorrentPath(SavePath, Name) — the not-yet-completed arm.
	body := `[{"hash":"def","name":"Show.S01E02","size":1000,"progress":0.5,"state":"downloading","category":"tv","save_path":"/qb/downloads","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })
	q.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/qb/downloads",
		localPrefix:  "/sonarr/downloads",
	})

	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/sonarr/downloads/Show.S01E02"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want %q", got[0].OutputPath.FullPath(), want)
	}
}

func TestQBittorrent_GetStatus_RemapsOutputRootFolders(t *testing.T) {
	t.Parallel()
	_, srv := newPrefsServer(t, `{"save_path":"/qb/downloads","max_ratio_enabled":false,"max_seeding_time_enabled":false,"max_ratio_act":0}`)
	q := newQBitForStatus(t, srv, func(s *qbittorrent.Settings) { s.ApiKey = "k" })
	q.SetRemotePathMapper(&stubMapper{
		host:         settingsFor(t, srv).Host,
		remotePrefix: "/qb/downloads",
		localPrefix:  "/sonarr/downloads",
	})
	info, err := q.GetStatus(context.Background())
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

func TestQBittorrent_RemapErrorFallsBackToRawPath(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"abc","name":"X","size":1000,"progress":1.0,"state":"pausedUP","category":"tv","save_path":"/qb/x","content_path":"/qb/x/X","ratio":2.0,"ratio_limit":1.0,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })
	q.SetRemotePathMapper(&stubMapper{host: settingsFor(t, srv).Host, err: errors.New("rpm blew up")})
	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if want := "/qb/x/X"; got[0].OutputPath.FullPath() != want {
		t.Errorf("OutputPath = %q, want raw %q on remap error", got[0].OutputPath.FullPath(), want)
	}
}

// Compile-time guard.
var _ downloadclients.RemotePathMapper = (*stubMapper)(nil)
