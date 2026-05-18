package qbittorrent_test

import (
	"context"
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
// QBittorrent.MarkItemAsImported() + RequestAction() — slice 5.G1h.
// Ports C# QBittorrent.MarkItemAsImported at QBittorrent.cs:56-73
// + QBittorrentProxyV2.SetTorrentLabel/GetLabels at :196-210.
// ============================================================================

type markServer struct {
	setCategoryCalls atomic.Int32
	setCategoryForm  atomic.Value // url.Values
	setCategoryCode  atomic.Int32

	categoriesCalls atomic.Int32
	categoriesBody  string
	categoriesCode  atomic.Int32
}

func newMarkServer(t *testing.T) (*markServer, *httptest.Server) {
	h := &markServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/setCategory":
			h.setCategoryCalls.Add(1)
			_ = r.ParseForm()
			h.setCategoryForm.Store(r.PostForm)
			code := int(h.setCategoryCode.Load())
			if code == 0 {
				code = http.StatusOK
			}
			w.WriteHeader(code)
		case "/api/v2/torrents/categories":
			h.categoriesCalls.Add(1)
			code := int(h.categoriesCode.Load())
			if code == 0 {
				code = http.StatusOK
			}
			w.WriteHeader(code)
			body := h.categoriesBody
			if body == "" {
				body = "{}"
			}
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newQBitForMark(t *testing.T, srv *httptest.Server, mutate func(*qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(nil, s, proxy, nil)
}

func TestMarkItemAsImported_NilItem(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	if err := q.MarkItemAsImported(context.Background(), nil); err == nil {
		t.Fatal("MarkItemAsImported(nil) err = nil, want error")
	}
}

func TestMarkItemAsImported_NilProxy(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	err := q.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "abc"})
	if err == nil {
		t.Fatal("MarkItemAsImported(nil proxy) err = nil, want error")
	}
}

func TestMarkItemAsImported_EmptyImportedCategorySkipsRPC(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = ""
	})
	err := q.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABC"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.setCategoryCalls.Load() != 0 {
		t.Errorf("setCategory fired %d times, want 0 (empty TvImportedCategory)", h.setCategoryCalls.Load())
	}
}

func TestMarkItemAsImported_SameCategorySkipsRPC(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = "tv-sonarr"
	})
	err := q.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABC"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.setCategoryCalls.Load() != 0 {
		t.Errorf("setCategory fired %d times, want 0 (imported==current)", h.setCategoryCalls.Load())
	}
}

func TestMarkItemAsImported_HappyPath(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.TvCategory = "tv-sonarr"
		s.TvImportedCategory = "tv-imported"
	})
	err := q.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "ABCDEF"})
	if err != nil {
		t.Fatalf("MarkItemAsImported() err = %v", err)
	}
	if h.setCategoryCalls.Load() != 1 {
		t.Fatalf("setCategory fired %d times, want 1", h.setCategoryCalls.Load())
	}
	form, _ := h.setCategoryForm.Load().(url.Values)
	if form == nil {
		t.Fatal("setCategory form missing")
	}
	if got := form.Get("hashes"); got != "abcdef" {
		t.Errorf("hashes = %q, want lowercase abcdef", got)
	}
	if got := form.Get("category"); got != "tv-imported" {
		t.Errorf("category = %q, want tv-imported", got)
	}
}

func TestMarkItemAsImported_500SwallowedNotPropagated(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	h.setCategoryCode.Store(http.StatusInternalServerError)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.TvImportedCategory = "tv-imported"
	})
	// C# :66-71 catches DownloadClientException and warn-logs;
	// does NOT propagate. The Go port matches.
	err := q.MarkItemAsImported(context.Background(),
		&downloadclients.Item{DownloadID: "abc"})
	if err != nil {
		t.Errorf("MarkItemAsImported() err = %v, want nil (failure swallowed per C# :66-71)", err)
	}
}

// ============================================================================
// RequestAction("getCategories") — slice 5.G1h.
// ============================================================================

func TestRequestAction_GetCategoriesReturnsList(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	h.categoriesBody = `{
		"tv-sonarr":{"name":"tv-sonarr","savePath":"/downloads/tv"},
		"radarr":{"name":"radarr","savePath":"/downloads/movies"}
	}`
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	got, err := q.RequestAction(context.Background(), "getCategories", nil)
	if err != nil {
		t.Fatalf("RequestAction(getCategories) err = %v", err)
	}
	labels, ok := got.([]qbittorrent.QBittorrentLabel)
	if !ok {
		t.Fatalf("RequestAction(getCategories) = %T, want []QBittorrentLabel", got)
	}
	if len(labels) != 2 {
		t.Fatalf("len = %d, want 2", len(labels))
	}
	// Order is unspecified — assert membership.
	seen := map[string]string{}
	for _, l := range labels {
		seen[l.Name] = l.SavePath
	}
	if seen["tv-sonarr"] != "/downloads/tv" || seen["radarr"] != "/downloads/movies" {
		t.Errorf("labels = %v, want tv-sonarr+radarr with savePaths", seen)
	}
	if h.categoriesCalls.Load() != 1 {
		t.Errorf("/categories fired %d times, want 1", h.categoriesCalls.Load())
	}
}

func TestRequestAction_GetCategoriesEmpty(t *testing.T) {
	t.Parallel()
	_, srv := newMarkServer(t)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	got, err := q.RequestAction(context.Background(), "getCategories", nil)
	if err != nil {
		t.Fatalf("RequestAction(getCategories) err = %v", err)
	}
	labels, ok := got.([]qbittorrent.QBittorrentLabel)
	if !ok {
		t.Fatalf("RequestAction(getCategories) = %T, want []QBittorrentLabel", got)
	}
	if len(labels) != 0 {
		t.Errorf("len = %d, want 0", len(labels))
	}
}

func TestRequestAction_GetCategoriesNilProxyErrors(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.RequestAction(context.Background(), "getCategories", nil)
	if err == nil {
		t.Fatal("RequestAction(getCategories, nil proxy) err = nil, want error")
	}
}

func TestRequestAction_GetCategoriesUpstream500(t *testing.T) {
	t.Parallel()
	h, srv := newMarkServer(t)
	h.categoriesCode.Store(http.StatusInternalServerError)
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.RequestAction(context.Background(), "getCategories", nil)
	if err == nil {
		t.Fatal("RequestAction(getCategories, 500) err = nil, want error")
	}
}

func TestRequestAction_LabelFallsBackToMapKey(t *testing.T) {
	t.Parallel()
	// Some qBittorrent builds omit the 'name' field in the value
	// and rely on the map key.
	h, srv := newMarkServer(t)
	h.categoriesBody = `{"only-key":{"savePath":"/dl"}}`
	q := newQBitForMark(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	got, err := q.RequestAction(context.Background(), "getCategories", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	labels := got.([]qbittorrent.QBittorrentLabel)
	if len(labels) != 1 || labels[0].Name != "only-key" || labels[0].SavePath != "/dl" {
		t.Errorf("labels = %+v, want [{only-key, /dl}]", labels)
	}
}
