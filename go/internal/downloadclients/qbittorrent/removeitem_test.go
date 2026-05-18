package qbittorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// ============================================================================
// QBittorrent.RemoveItem() — slice 5.G1f. Ports C# QBittorrent
// RemoveItem at QBittorrent.cs:361-364 + QBittorrentProxyV2
// RemoveTorrent at :183-194.
// ============================================================================

type deleteServer struct {
	calls      atomic.Int32
	form       atomic.Value // url.Values
	respStatus atomic.Int32 // 0 = default 200
}

func newDeleteServer(t *testing.T) (*deleteServer, *httptest.Server) {
	h := &deleteServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/delete":
			h.calls.Add(1)
			_ = r.ParseForm()
			h.form.Store(r.PostForm)
			status := int(h.respStatus.Load())
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newQBitForRemove(t *testing.T, srv *httptest.Server, mutate func(*qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(&downloadclients.DownloadClient{ID: 1, Name: "qb"}, s, proxy, nil)
}

func TestRemoveItem_NilItem(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	if err := q.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("RemoveItem(nil) err = nil, want error")
	}
}

func TestRemoveItem_NilProxy(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	err := q.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false)
	if err == nil {
		t.Fatal("RemoveItem(nil proxy) err = nil, want error")
	}
}

func TestRemoveItem_DeleteDataFalseOmitsField(t *testing.T) {
	t.Parallel()
	h, srv := newDeleteServer(t)
	q := newQBitForRemove(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	err := q.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "ABCDEF"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("delete fired %d times, want 1", h.calls.Load())
	}
	form, _ := h.form.Load().(url.Values)
	if got := form.Get("hashes"); got != "abcdef" {
		t.Errorf("hashes = %q, want lowercase abcdef", got)
	}
	if form.Has("deleteFiles") {
		t.Errorf("deleteFiles field present when deleteData=false; should be omitted")
	}
}

func TestRemoveItem_DeleteDataTrueSendsTrue(t *testing.T) {
	t.Parallel()
	h, srv := newDeleteServer(t)
	q := newQBitForRemove(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	err := q.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "AB12CD"}, true)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	form, _ := h.form.Load().(url.Values)
	if got := form.Get("hashes"); got != "ab12cd" {
		t.Errorf("hashes = %q, want ab12cd", got)
	}
	if got := form.Get("deleteFiles"); got != "true" {
		t.Errorf("deleteFiles = %q, want true", got)
	}
}

func TestRemoveItem_Server500SurfacesUnexpected(t *testing.T) {
	t.Parallel()
	h, srv := newDeleteServer(t)
	h.respStatus.Store(http.StatusInternalServerError)
	q := newQBitForRemove(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	err := q.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abc"}, false)
	if !errors.Is(err, qbittorrent.ErrUnexpected) {
		t.Errorf("RemoveItem() err = %v, want ErrUnexpected", err)
	}
}

func TestRemoveItem_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	q := newQBitForRemove(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	err := q.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abc"}, false)
	if err == nil {
		t.Fatal("RemoveItem() err = nil, want auth failure")
	}
}
