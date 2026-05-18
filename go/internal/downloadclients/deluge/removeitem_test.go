package deluge_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
)

// ============================================================================
// Deluge.RemoveItem() — slice 5.G2f. Ports C# Deluge.RemoveItem at
// Deluge.cs:217-220 + DelugeProxy.RemoveTorrent at :152-156.
// ============================================================================

type removeServer struct {
	calls         atomic.Int32
	params        atomic.Value // []any
	rpcErrMessage string       // when non-empty, RPC returns error
}

func newRemoveServer(t *testing.T) (*removeServer, *httptest.Server) {
	h := &removeServer{}
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
		case "core.remove_torrent":
			h.calls.Add(1)
			var p []any
			_ = json.Unmarshal(env.Params, &p)
			h.params.Store(p)
			if h.rpcErrMessage != "" {
				_, _ = io.WriteString(w, `{"result":null,"error":{"code":7,"message":"`+h.rpcErrMessage+`"},"id":2}`)
				return
			}
			_, _ = io.WriteString(w, `{"result":true,"error":null,"id":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newDelugeForRemove(t *testing.T, srv *httptest.Server) *deluge.Deluge {
	t.Helper()
	s := settingsFor(t, srv)
	proxy, err := deluge.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return deluge.New(nil, s, proxy, nil)
}

func TestRemoveItem_NilItem(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	if err := d.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("RemoveItem(nil) err = nil, want error")
	}
}

func TestRemoveItem_NilProxy(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	err := d.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false)
	if err == nil {
		t.Fatal("RemoveItem(nil proxy) err = nil, want error")
	}
}

func TestRemoveItem_DeleteDataFalse(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	d := newDelugeForRemove(t, srv)
	err := d.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "ABCDEF"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("core.remove_torrent fired %d times, want 1", h.calls.Load())
	}
	params, _ := h.params.Load().([]any)
	if len(params) != 2 {
		t.Fatalf("params = %v, want 2", params)
	}
	if params[0] != "abcdef" {
		t.Errorf("hash = %v, want lowercase abcdef", params[0])
	}
	if params[1] != false {
		t.Errorf("deleteData = %v, want false", params[1])
	}
}

func TestRemoveItem_DeleteDataTrue(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	d := newDelugeForRemove(t, srv)
	err := d.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "DEAD123"}, true)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	params, _ := h.params.Load().([]any)
	if params[0] != "dead123" {
		t.Errorf("hash = %v, want dead123", params[0])
	}
	if params[1] != true {
		t.Errorf("deleteData = %v, want true", params[1])
	}
}

func TestRemoveItem_RPCFailureBubbles(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.rpcErrMessage = "torrent not found"
	d := newDelugeForRemove(t, srv)
	err := d.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abc"}, false)
	if !errors.Is(err, deluge.ErrRPCFailure) {
		t.Errorf("RemoveItem() err = %v, want ErrRPCFailure", err)
	}
}

func TestRemoveItem_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// auth.login returns false → ErrAuthFailed.
		_, _ = io.WriteString(w, `{"result":false,"error":null,"id":1}`)
	}))
	defer srv.Close()
	d := newDelugeForRemove(t, srv)
	err := d.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abc"}, false)
	if !errors.Is(err, deluge.ErrAuthFailed) {
		t.Errorf("RemoveItem() err = %v, want ErrAuthFailed", err)
	}
}
