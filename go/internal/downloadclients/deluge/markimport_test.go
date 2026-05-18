package deluge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.MarkItemAsImported() — slice 5.G2h. Ports C#
// Deluge.MarkItemAsImported at Deluge.cs:39-56 +
// DelugeProxy.SetTorrentLabel at :189-192. Closes the 5.G2 arc.
// ============================================================================

type markServer struct {
	labelCalls    atomic.Int32
	labelParams   atomic.Value // []any
	rpcErrMessage string
}

func newMarkServer(t *testing.T) (*markServer, *httptest.Server) {
	h := &markServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		switch env.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "sid"})
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":1}`)
		case "label.set_torrent":
			h.labelCalls.Add(1)
			var p []any
			_ = json.Unmarshal(env.Params, &p)
			h.labelParams.Store(p)
			if h.rpcErrMessage != "" {
				_, _ = io.WriteString(w, `{"result":null,"error":{"code":7,"message":"`+h.rpcErrMessage+`"},"id":2}`)
				return
			}
			_, _ = io.WriteString(w, `{"result":null,"error":null,"id":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newDelugeForMark(t *testing.T, srv *httptest.Server, mutate func(*deluge.Settings)) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(nil, s, proxy, nil)
}

func TestMarkItemAsImported_NilItem(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	if err := d.MarkItemAsImported(context.Background(), nil); err == nil {
		t.Fatal("MarkItemAsImported(nil) err = nil, want error")
	}
}

func TestMarkItemAsImported_NilProxy(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	err := d.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "abc"})
	if err == nil {
		t.Fatal("MarkItemAsImported(nil proxy) err = nil, want error")
	}
}

func TestMarkItemAsImported_EmptyImportedCategorySkipsRPC(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	d := newDelugeForMark(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = ""
	})
	err := d.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABC"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.labelCalls.Load() != 0 {
		t.Errorf("label.set_torrent fired %d times, want 0 (empty TvImportedCategory)", h.labelCalls.Load())
	}
}

func TestMarkItemAsImported_SameCategorySkipsRPC(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	d := newDelugeForMark(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = "tv-sonarr"
	})
	err := d.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABC"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.labelCalls.Load() != 0 {
		t.Errorf("label.set_torrent fired despite imported==current")
	}
}

func TestMarkItemAsImported_HappyPath(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	d := newDelugeForMark(t, srv, func(s *deluge.Settings) {
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = "tv-imported"
	})
	err := d.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABCDEF"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.labelCalls.Load() != 1 {
		t.Fatalf("label.set_torrent fired %d times, want 1", h.labelCalls.Load())
	}
	params, _ := h.labelParams.Load().([]any)
	if len(params) != 2 {
		t.Fatalf("params = %v, want 2", params)
	}
	if params[0] != "abcdef" {
		t.Errorf("hash = %v, want lowercase abcdef", params[0])
	}
	if params[1] != "tv-imported" {
		t.Errorf("label = %v, want tv-imported", params[1])
	}
}

func TestMarkItemAsImported_FailureSwallowedNotPropagated(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	h.rpcErrMessage = "label not found"
	d := newDelugeForMark(t, srv, func(s *deluge.Settings) {
		s.TvImportedCategory = "tv-imported"
	})
	// C# :49-54 catches DownloadClientUnavailableException + warn-logs;
	// does NOT propagate. The Go port matches.
	err := d.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "abc"})
	if err != nil {
		t.Errorf("MarkItemAsImported() err = %v, want nil (failure swallowed per C# :49-54)", err)
	}
}
