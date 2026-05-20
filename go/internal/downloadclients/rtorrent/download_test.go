package rtorrent_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/rtorrent"
)

// loadOKResponse is the standard `<int>0</int>` success envelope
// rTorrent returns from load.start / load.raw_start when the add
// succeeds.
const loadOKResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><int>0</int></value></param></params></methodResponse>`

const loadFailResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><int>-501</int></value></param></params></methodResponse>`

func TestDownload_MagnetURL_UsesLoadStart(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)

	hash, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		MagnetURL:    "magnet:?xt=urn:btih:DEADBEEF&dn=Show",
		IsRecent:     false,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hash != "DEADBEEF" {
		t.Errorf("hash: got %q, want DEADBEEF (uppercased from magnet xt)", hash)
	}
	if m, _ := h.lastMethod.Load().(string); m != "load.start" {
		t.Errorf("method: got %q, want load.start", m)
	}
}

func TestDownload_AddStopped_UsesLoadNormal(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.AddStopped = true
	rt := rtorrent.New(nil, s, p, nil)

	if _, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:CAFE",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "load.normal" {
		t.Errorf("method: got %q, want load.normal (AddStopped=true)", m)
	}
}

func TestDownload_TorrentBody_UsesLoadRawStart(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	rt := rtorrent.New(nil, s, p, nil)

	hash, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E02",
		InfoHash:     "abc123",
		TorrentBody:  []byte("fake-torrent-bytes"),
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hash != "ABC123" {
		t.Errorf("hash: got %q, want ABC123 (uppercased from InfoHash)", hash)
	}
	if m, _ := h.lastMethod.Load().(string); m != "load.raw_start" {
		t.Errorf("method: got %q, want load.raw_start", m)
	}
}

func TestDownload_TorrentBody_AddStoppedUsesLoadRaw(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.AddStopped = true
	rt := rtorrent.New(nil, s, p, nil)

	if _, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		InfoHash:    "feed",
		TorrentBody: []byte("data"),
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "load.raw" {
		t.Errorf("method: got %q, want load.raw (AddStopped=true)", m)
	}
}

func TestDownload_DownloadURL_UsesLoadStart(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	rt := rtorrent.New(nil, s, p, nil)

	if _, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		InfoHash:    "1234",
		DownloadURL: "https://indexer.example/get/show.torrent",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "load.start" {
		t.Errorf("method: got %q, want load.start", m)
	}
}

func TestDownload_NonZeroResponseSurfacesError(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadFailResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)

	if _, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	}); err == nil {
		t.Errorf("Download: want error on non-zero load response")
	}
}

func TestDownload_MissingHashAndMagnetErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)

	// No InfoHash, no MagnetURL, but DownloadURL set → can't extract hash.
	_, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		DownloadURL: "https://indexer.example/get/show.torrent",
	})
	if err == nil {
		t.Errorf("Download: want error (no InfoHash, no magnet)")
	}
}

func TestDownload_EmptyRequestErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, loadOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)

	// No source URL/magnet/body — but InfoHash set so hash resolution succeeds.
	_, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		InfoHash: "abc",
	})
	if err == nil {
		t.Errorf("Download: want error (no source URL/magnet/body)")
	}
}

func TestDownload_NilRequestErrors(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	if _, err := rt.Download(context.Background(), nil); err == nil {
		t.Errorf("Download(nil): want error")
	}
}

func TestDownload_NilProxyErrors(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	_, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:CAFE",
	})
	if err == nil {
		t.Errorf("Download: want error when no proxy is wired")
	}
}

func TestDownload_FaultPropagates(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, sampleFaultResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)

	_, err := rt.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:FEED",
	})
	if err == nil {
		t.Fatalf("Download: want fault error")
	}
	var fault *rtorrent.FaultError
	if !errors.As(err, &fault) {
		t.Errorf("error type: got %T, want *FaultError", err)
	}
}

func TestDCommandsArgs_AppliedInBody(t *testing.T) {
	t.Parallel()
	// Spot-check that label / priority / directory appear in the
	// methodCall body as d.<command>.set= strings. The xmlrpcServer
	// fake records the lastMethod but not the args; capture the
	// body separately by spinning a custom recorder.
	bodies := make([]string, 0, 4)
	h := newXMLRPCServerCapture(t, loadOKResponse, &bodies)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	s.TvDirectory = "/data/downloads"
	rt := rtorrent.New(nil, s, p, nil)
	_, _ = rt.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:1234",
		IsRecent:  false, // OlderTvPriority — Normal default → no d.priority.set
	})
	if len(bodies) != 1 {
		t.Fatalf("expected 1 captured body, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "d.custom1.set=tv-sonarr") {
		t.Errorf("body missing d.custom1.set=tv-sonarr: %s", bodies[0])
	}
	if !strings.Contains(bodies[0], "d.directory.set=/data/downloads") {
		t.Errorf("body missing d.directory.set=/data/downloads: %s", bodies[0])
	}
	if strings.Contains(bodies[0], "d.priority.set=") {
		t.Errorf("body contains d.priority.set even though priority == Normal: %s", bodies[0])
	}
}

func TestDCommandsArgs_PriorityNonNormalEmits(t *testing.T) {
	t.Parallel()
	bodies := make([]string, 0, 4)
	h := newXMLRPCServerCapture(t, loadOKResponse, &bodies)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.RecentTvPriority = int(rtorrent.PriorityHigh)
	rt := rtorrent.New(nil, s, p, nil)
	_, _ = rt.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:1234",
		IsRecent:  true,
	})
	if len(bodies) != 1 {
		t.Fatalf("expected 1 captured body, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "d.priority.set=3") {
		t.Errorf("body missing d.priority.set=3 (PriorityHigh): %s", bodies[0])
	}
}
