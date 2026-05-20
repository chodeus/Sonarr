package rtorrent_test

import (
	"context"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/rtorrent"
)

// eraseOKResponse — d.erase returns <int>0</int> on success.
const eraseOKResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><int>0</int></value></param></params></methodResponse>`

const eraseFailResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><int>-501</int></value></param></params></methodResponse>`

func TestRemoveItem_FiresDErase(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, eraseOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	if err := rt.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC123"}, false); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "d.erase" {
		t.Errorf("method: got %q, want d.erase", m)
	}
}

func TestRemoveItem_NonZeroResponseErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, eraseFailResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	if err := rt.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false); err == nil {
		t.Errorf("RemoveItem: want error on non-zero response")
	}
}

func TestRemoveItem_DeleteDataLogsWarning(t *testing.T) {
	t.Parallel()
	// The deleteData arm is deferred — RemoveItem should still
	// complete (only removes from queue) and log a warning.
	h := newXMLRPCServer(t, eraseOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	if err := rt.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, true); err != nil {
		t.Errorf("RemoveItem with deleteData=true should not error: %v", err)
	}
}

func TestRemoveItem_NilItemErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, eraseOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	if err := rt.RemoveItem(context.Background(), nil, false); err == nil {
		t.Errorf("RemoveItem(nil): want error")
	}
}

func TestRemoveItem_EmptyDownloadIDErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, eraseOKResponse)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	if err := rt.RemoveItem(context.Background(), &downloadclients.Item{}, false); err == nil {
		t.Errorf("RemoveItem(empty ID): want error")
	}
}

// labelResponse — d.custom1.set echoes back the label on success.
func labelResponse(label string) string {
	return `<?xml version="1.0"?>
<methodResponse><params><param><value><string>` + label + `</string></value></param></params></methodResponse>`
}

func TestMarkItemAsImported_AppliesPostImportLabel(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, labelResponse("tv-done"))
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	s.TvImportedCategory = "tv-done"
	rt := rtorrent.New(nil, s, p, nil)
	if err := rt.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "d.custom1.set" {
		t.Errorf("method: got %q, want d.custom1.set", m)
	}
}

func TestMarkItemAsImported_NoOpWhenLabelMatchesTvCategory(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, labelResponse("tv-sonarr"))
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	s.TvImportedCategory = "tv-sonarr" // same as TvCategory
	rt := rtorrent.New(nil, s, p, nil)
	if err := rt.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if h.calls.Load() != 0 {
		t.Errorf("expected 0 XML-RPC calls when label matches TvCategory, got %d", h.calls.Load())
	}
}

func TestMarkItemAsImported_NoOpWhenEmpty(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, labelResponse(""))
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvImportedCategory = "" // not configured
	rt := rtorrent.New(nil, s, p, nil)
	if err := rt.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC"}); err != nil {
		t.Fatalf("MarkItemAsImported: %v", err)
	}
	if h.calls.Load() != 0 {
		t.Errorf("expected 0 calls when TvImportedCategory unset, got %d", h.calls.Load())
	}
}

func TestMarkItemAsImported_LabelFailureIsBestEffort(t *testing.T) {
	t.Parallel()
	// Server returns the wrong label → mismatch error from proxy.
	// MarkItemAsImported should absorb it and return nil.
	h := newXMLRPCServer(t, labelResponse("WRONG-LABEL"))
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	s.TvImportedCategory = "tv-done"
	rt := rtorrent.New(nil, s, p, nil)
	if err := rt.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "ABC", Title: "Show.S01E01"}); err != nil {
		t.Errorf("MarkItemAsImported: best-effort failure should be absorbed; got %v", err)
	}
}

func TestGetStatus_IsLocalhostForLocalhostHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		host := host
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			s := rtorrent.NewSettings()
			s.Host = host
			rt := rtorrent.New(nil, s, nil, nil)
			info, err := rt.GetStatus(context.Background())
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if info == nil || !info.IsLocalhost {
				t.Errorf("Host=%q: IsLocalhost = %v, want true", host, info)
			}
		})
	}
}

func TestGetStatus_NotLocalhostForRemoteHost(t *testing.T) {
	t.Parallel()
	s := rtorrent.NewSettings()
	s.Host = "rtor.example.com"
	rt := rtorrent.New(nil, s, nil, nil)
	info, err := rt.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if info == nil || info.IsLocalhost {
		t.Errorf("remote host: IsLocalhost = %v, want false", info)
	}
}

// Smoke-test the proxy methods directly so the test surface
// covers the wire-level hash lower-case + label arg shape without
// going through the Provider.
func TestProxy_RemoveTorrentLowercasesHash(t *testing.T) {
	t.Parallel()
	bodies := make([]string, 0, 1)
	h := newXMLRPCServerCapture(t, eraseOKResponse, &bodies)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	if err := p.RemoveTorrent(context.Background(), "ABC123", h.settings("RPC2")); err != nil {
		t.Fatalf("RemoveTorrent: %v", err)
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], "<string>abc123</string>") {
		t.Errorf("RemoveTorrent body: %s (want lowercase hash)", bodies)
	}
}

func TestProxy_SetTorrentLabelSendsArgs(t *testing.T) {
	t.Parallel()
	bodies := make([]string, 0, 1)
	h := newXMLRPCServerCapture(t, labelResponse("tv-done"), &bodies)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	if err := p.SetTorrentLabel(context.Background(), "ABC", "tv-done", h.settings("RPC2")); err != nil {
		t.Fatalf("SetTorrentLabel: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("captured %d bodies, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "<string>abc</string>") {
		t.Errorf("SetTorrentLabel: missing lowercase hash arg")
	}
	if !strings.Contains(bodies[0], "<string>tv-done</string>") {
		t.Errorf("SetTorrentLabel: missing label arg")
	}
}

func TestProxy_SetTorrentLabelMismatchErrors(t *testing.T) {
	t.Parallel()
	h := newXMLRPCServer(t, labelResponse("WRONG"))
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	if err := p.SetTorrentLabel(context.Background(), "ABC", "tv-done", h.settings("RPC2")); err == nil {
		t.Errorf("SetTorrentLabel: want error when echo mismatches")
	}
}
