package aria2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/aria2"
)

// gidResponse — aria2.addUri / addTorrent return the GID as a
// bare <string>...</string> response.
func gidResponse(gid string) string {
	return `<?xml version="1.0"?>
<methodResponse><params><param><value><string>` + gid + `</string></value></param></params></methodResponse>`
}

func TestDownload_MagnetURL_FiresAddUri(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("a1b2c3d4"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	gid, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEADBEEF",
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if gid != "A1B2C3D4" {
		t.Errorf("gid: got %q, want A1B2C3D4 (uppercased)", gid)
	}
	if m, _ := h.lastMethod.Load().(string); m != "aria2.addUri" {
		t.Errorf("method: got %q, want aria2.addUri", m)
	}
}

func TestDownload_DownloadURL_FiresAddUri(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid-direct-url"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		DownloadURL: "https://indexer.example/torrent",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "aria2.addUri" {
		t.Errorf("method: got %q, want aria2.addUri", m)
	}
}

func TestDownload_TorrentBody_FiresAddTorrent(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid-from-file"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  []byte("fake-torrent-bytes"),
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if m, _ := h.lastMethod.Load().(string); m != "aria2.addTorrent" {
		t.Errorf("method: got %q, want aria2.addTorrent", m)
	}
}

func TestDownload_DirectoryAppliedAsDirOption(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.Directory = "/data/downloads"
	a := aria2.New(nil, s, p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	body, _ := h.lastBody.Load().(string)
	// Verify the dir struct member appears in the body — the
	// options dict is the third arg of aria2.addUri.
	if !strings.Contains(body, "<name>dir</name>") || !strings.Contains(body, "<string>/data/downloads</string>") {
		t.Errorf("body missing dir option: %s", body)
	}
}

func TestDownload_NoDirectoryNoDirOption(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.Directory = "" // no override
	a := aria2.New(nil, s, p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	body, _ := h.lastBody.Load().(string)
	if strings.Contains(body, "<name>dir</name>") {
		t.Errorf("body should not contain dir option when Directory empty: %s", body)
	}
}

func TestDownload_TokenAppliedAsFirstArg(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	s.SecretToken = "hunter2"
	a := aria2.New(nil, s, p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	body, _ := h.lastBody.Load().(string)
	if !strings.Contains(body, "<string>token:hunter2</string>") {
		t.Errorf("body missing token arg: %s", body)
	}
}

func TestDownload_EmptyGIDInResponseErrors(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse(""))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	}); err == nil {
		t.Errorf("Download: want error on empty GID response")
	}
}

func TestDownload_FaultPropagates(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, sampleFaultResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	_, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	})
	if err == nil {
		t.Fatalf("Download: want fault error")
	}
	var fault *aria2.FaultError
	if !errors.As(err, &fault) {
		t.Errorf("error type: got %T, want *FaultError", err)
	}
}

func TestDownload_NilRequestErrors(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	if _, err := a.Download(context.Background(), nil); err == nil {
		t.Errorf("Download(nil): want error")
	}
}

func TestDownload_NilProxyErrors(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	_, err := a.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:DEAD",
	})
	if err == nil {
		t.Errorf("Download: want error when no proxy")
	}
}

func TestDownload_MissingSourceErrors(t *testing.T) {
	t.Parallel()
	h := newAria2Server(t, gidResponse("gid"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if _, err := a.Download(context.Background(), &downloadclients.GrabRequest{}); err == nil {
		t.Errorf("Download(no source): want error")
	}
}
