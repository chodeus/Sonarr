package qbittorrent_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// ============================================================================
// QBittorrent.GetItems() — slice 5.G1d. Ports the projection at C#
// QBittorrent.GetItems / QBittorrentProxyV2.GetTorrents.
// ============================================================================

// torrentsHandler returns a handler that serves
// /api/v2/auth/login + /api/v2/torrents/info with caller-supplied
// JSON body.
type torrentsHandler struct {
	loginCookie string
	infoBody    string
	infoStatus  int

	loginCalls atomic.Int32
	infoCalls  atomic.Int32
	infoQuery  atomic.Value // string
}

func (h *torrentsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/auth/login":
		h.loginCalls.Add(1)
		if h.loginCookie != "" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: h.loginCookie})
		}
		_, _ = io.WriteString(w, "Ok.")
	case "/api/v2/torrents/info":
		h.infoCalls.Add(1)
		h.infoQuery.Store(r.URL.RawQuery)
		status := h.infoStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		body := h.infoBody
		if body == "" {
			body = "[]"
		}
		_, _ = io.WriteString(w, body)
	default:
		http.NotFound(w, r)
	}
}

func mustNewQBit(t *testing.T, srv *httptest.Server, mutate func(s *qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(&downloadclients.DownloadClient{ID: 42, Name: "qbit-test"}, s, proxy, nil)
}

func TestGetItems_EmptyArray(t *testing.T) {
	t.Parallel()
	h := &torrentsHandler{loginCookie: "sid", infoBody: "[]"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if got == nil {
		t.Fatal("GetItems() = nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("GetItems() = %v, want empty slice", got)
	}
}

func TestGetItems_TwoTorrents_ProjectsCorrectly(t *testing.T) {
	t.Parallel()
	body := `[
		{"hash":"abc123","name":"Show.S01E01","size":2147483648,"progress":0.5,"eta":120,"state":"downloading","category":"tv-sonarr","save_path":"/downloads","content_path":"/downloads/Show.S01E01","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2,"last_activity":1700000000},
		{"hash":"def456","name":"Show.S01E02","size":1073741824,"progress":1.0,"eta":8640000,"state":"pausedUP","category":"tv-sonarr","save_path":"/downloads","content_path":"/downloads/Show.S01E02","ratio":1.5,"ratio_limit":1.0,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2,"last_activity":1700000100}
	]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetItems() len = %d, want 2", len(got))
	}

	// Item 0: downloading, mid-progress
	d := got[0]
	if d.DownloadID != "ABC123" {
		t.Errorf("[0].DownloadID = %q, want %q", d.DownloadID, "ABC123")
	}
	if d.Title != "Show.S01E01" {
		t.Errorf("[0].Title = %q", d.Title)
	}
	if d.TotalSize != 2147483648 {
		t.Errorf("[0].TotalSize = %d, want 2147483648", d.TotalSize)
	}
	if d.RemainingSize != 1073741824 {
		t.Errorf("[0].RemainingSize = %d, want 1073741824 (size * (1-progress))", d.RemainingSize)
	}
	if d.Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("[0].Status = %v, want Downloading", d.Status)
	}
	if d.Category != "tv-sonarr" {
		t.Errorf("[0].Category = %q", d.Category)
	}
	if d.RemainingTime == nil || *d.RemainingTime != 120*time.Second {
		t.Errorf("[0].RemainingTime = %v, want 120s", d.RemainingTime)
	}
	if d.SeedRatio == nil || *d.SeedRatio != 0 {
		t.Errorf("[0].SeedRatio = %v, want 0", d.SeedRatio)
	}
	if d.DownloadClientInfo == nil {
		t.Fatal("[0].DownloadClientInfo nil")
	}
	if d.DownloadClientInfo.Type != qbittorrent.Implementation {
		t.Errorf("[0].DownloadClientInfo.Type = %q, want %q", d.DownloadClientInfo.Type, qbittorrent.Implementation)
	}
	if d.DownloadClientInfo.ID != 42 {
		t.Errorf("[0].DownloadClientInfo.ID = %d, want 42", d.DownloadClientInfo.ID)
	}

	// Item 1: completed, paused after upload
	c := got[1]
	if c.DownloadID != "DEF456" {
		t.Errorf("[1].DownloadID = %q", c.DownloadID)
	}
	if c.Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("[1].Status = %v, want Completed", c.Status)
	}
	// ETA clamped to zero for completed torrents.
	if c.RemainingTime == nil || *c.RemainingTime != 0 {
		t.Errorf("[1].RemainingTime = %v, want 0", c.RemainingTime)
	}
	// ContentPath != SavePath → OutputPath = ContentPath.
	if c.OutputPath.String() != "/downloads/Show.S01E02" {
		t.Errorf("[1].OutputPath = %q, want /downloads/Show.S01E02", c.OutputPath.String())
	}
	// ratio=1.5 >= ratio_limit=1.0 → hasReachedSeedLimit returns
	// true. But CanBeRemoved also requires RemoveCompletedDownloads
	// on the DownloadClient row, which is false in the default test
	// fixture. Verify the AND-gating.
	if c.CanBeRemoved {
		t.Errorf("[1].CanBeRemoved = true, want false (def.RemoveCompletedDownloads=false)")
	}
}

func TestGetItems_RemoveCompletedDownloadsGating(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"abc","name":"X","size":1000,"progress":1.0,"state":"pausedUP","category":"tv","save_path":"/dl","content_path":"/dl/X","ratio":2.0,"ratio_limit":1.0,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()

	s := settingsFor(t, srv)
	s.Username = "u"
	s.Password = "p"
	proxy, _ := qbittorrent.NewProxy(srv.Client(), nil)
	def := &downloadclients.DownloadClient{ID: 1, Name: "qb", RemoveCompletedDownloads: true}
	q := qbittorrent.New(def, s, proxy, nil)

	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(got))
	}
	if !got[0].CanBeRemoved {
		t.Errorf("CanBeRemoved = false, want true (pausedUP + ratio>=limit + RemoveCompletedDownloads=true)")
	}
	if !got[0].CanMoveFiles {
		t.Errorf("CanMoveFiles = false, want true (mirrors CanBeRemoved)")
	}
}

func TestGetItems_CategoryFilterAppended(t *testing.T) {
	t.Parallel()
	h := &torrentsHandler{loginCookie: "sid", infoBody: "[]"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.TvCategory = "tv-sonarr"
	})
	if _, err := q.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	got := h.infoQuery.Load().(string)
	if got != "category=tv-sonarr" {
		t.Errorf("infoQuery = %q, want category=tv-sonarr", got)
	}
}

func TestGetItems_NoCategoryFilterWhenEmpty(t *testing.T) {
	t.Parallel()
	h := &torrentsHandler{loginCookie: "sid", infoBody: "[]"}
	srv := httptest.NewServer(h)
	defer srv.Close()

	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.TvCategory = ""
	})
	if _, err := q.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	got := h.infoQuery.Load().(string)
	if got != "" {
		t.Errorf("infoQuery = %q, want empty", got)
	}
}

func TestGetItems_StatusMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state      string
		wantStatus downloadclients.DownloadItemStatus
		wantMsg    string // substring; empty means no specific check
	}{
		{"error", downloadclients.DownloadItemStatusWarning, "error"},
		{"missingFiles", downloadclients.DownloadItemStatusWarning, "missing"},
		{"stalledDL", downloadclients.DownloadItemStatusWarning, "stalled"},
		{"pausedDL", downloadclients.DownloadItemStatusPaused, ""},
		{"stoppedDL", downloadclients.DownloadItemStatusPaused, ""},
		{"queuedDL", downloadclients.DownloadItemStatusQueued, ""},
		{"checkingDL", downloadclients.DownloadItemStatusQueued, ""},
		{"checkingUP", downloadclients.DownloadItemStatusQueued, ""},
		{"checkingResumeData", downloadclients.DownloadItemStatusQueued, ""},
		{"uploading", downloadclients.DownloadItemStatusCompleted, ""},
		{"stalledUP", downloadclients.DownloadItemStatusCompleted, ""},
		{"queuedUP", downloadclients.DownloadItemStatusCompleted, ""},
		{"pausedUP", downloadclients.DownloadItemStatusCompleted, ""},
		{"stoppedUP", downloadclients.DownloadItemStatusCompleted, ""},
		{"forcedUP", downloadclients.DownloadItemStatusCompleted, ""},
		{"downloading", downloadclients.DownloadItemStatusDownloading, ""},
		{"forcedDL", downloadclients.DownloadItemStatusDownloading, ""},
		{"moving", downloadclients.DownloadItemStatusDownloading, ""},
		{"metaDL", downloadclients.DownloadItemStatusQueued, "metadata"},
		{"forcedMetaDL", downloadclients.DownloadItemStatusQueued, "metadata"},
		{"weirdNewState", downloadclients.DownloadItemStatusDownloading, "Unknown"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()
			body := `[{"hash":"h","name":"X","size":100,"progress":0.5,"state":"` + tc.state +
				`","category":"tv","save_path":"/dl","content_path":"/dl/X","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
			h := &torrentsHandler{loginCookie: "sid", infoBody: body}
			srv := httptest.NewServer(h)
			defer srv.Close()
			q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
				s.Username = "u"
				s.Password = "p"
			})
			got, err := q.GetItems(context.Background())
			if err != nil {
				t.Fatalf("GetItems() err = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len(items) = %d, want 1", len(got))
			}
			if got[0].Status != tc.wantStatus {
				t.Errorf("Status[%s] = %v, want %v", tc.state, got[0].Status, tc.wantStatus)
			}
			if tc.wantMsg != "" && !strings.Contains(strings.ToLower(got[0].Message), strings.ToLower(tc.wantMsg)) {
				t.Errorf("Message[%s] = %q, want substring %q", tc.state, got[0].Message, tc.wantMsg)
			}
		})
	}
}

func TestGetItems_ContentPathSameAsSavePathWarns(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"h","name":"X","size":100,"progress":1.0,"state":"pausedUP","category":"tv","save_path":"/dl","content_path":"/dl","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	got, err := q.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if got[0].Status != downloadclients.DownloadItemStatusWarning {
		t.Errorf("Status = %v, want Warning (ContentPath==SavePath)", got[0].Status)
	}
}

func TestGetItems_CategoryFallsBackToLabel(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"h","name":"X","size":100,"progress":0.1,"state":"downloading","label":"legacy-label","category":"","save_path":"/dl","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	got, _ := q.GetItems(context.Background())
	if got[0].Category != "legacy-label" {
		t.Errorf("Category = %q, want legacy-label", got[0].Category)
	}
}

func TestGetItems_HasPostImportCategoryFlag(t *testing.T) {
	t.Parallel()
	body := `[{"hash":"h","name":"X","size":100,"progress":0.1,"state":"downloading","category":"tv","save_path":"/dl","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`
	h := &torrentsHandler{loginCookie: "sid", infoBody: body}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.TvImportedCategory = "tv-imported"
	})
	got, _ := q.GetItems(context.Background())
	if !got[0].DownloadClientInfo.HasPostImportCategory {
		t.Error("HasPostImportCategory = false, want true (TvImportedCategory set)")
	}
}

func TestGetItems_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		// 401 won't trigger reauth-once (only 403 does) — so it
		// surfaces directly through GetTorrents as ErrAuthFailed.
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
	})
	_, err := q.GetItems(context.Background())
	if !errors.Is(err, qbittorrent.ErrAuthFailed) {
		t.Errorf("GetItems() err = %v, want ErrAuthFailed", err)
	}
}

func TestGetItems_ApiKeyAuthPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			t.Errorf("login fired despite ApiKey auth")
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "key"
	})
	if _, err := q.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
}

func TestGetItems_UpstreamServerErrorReturnsUnexpected(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.GetItems(context.Background())
	if !errors.Is(err, qbittorrent.ErrUnexpected) {
		t.Errorf("GetItems() err = %v, want ErrUnexpected", err)
	}
}

func TestGetItems_MalformedJSONReturnsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{not array}`)
	}))
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.GetItems(context.Background())
	if err == nil {
		t.Fatal("GetItems() err = nil, want parse error")
	}
}

// ============================================================================
// Proxy.GetVersion convenience helper — backstop for 5.G1d wiring.
// ============================================================================

func TestProxy_GetVersion_StripsVPrefix(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "v4.5.0\n")
	}))
	defer srv.Close()
	p, _ := qbittorrent.NewProxy(srv.Client(), nil)
	s := settingsFor(t, srv)
	s.ApiKey = "k"
	got, err := p.GetVersion(context.Background(), s)
	if err != nil {
		t.Fatalf("GetVersion() err = %v", err)
	}
	if got != "4.5.0" {
		t.Errorf("GetVersion() = %q, want %q", got, "4.5.0")
	}
}

// ============================================================================
// Slice 6.E: DHT-disabled-metaDL warning. When a torrent is in metaDL/
// forcedMetaDL state AND prefs.DhtEnabled=false, the metaDL arm
// promotes from Queued to Warning with a descriptive message.
// ============================================================================

// torrentsPrefsHandler serves both /api/v2/torrents/info and
// /api/v2/app/preferences in one server — needed so GetItems can hit
// the lazy GetConfig probe added in slice 6.E.
type torrentsPrefsHandler struct {
	loginCookie string
	infoBody    string
	prefsBody   string

	prefsCalls atomic.Int32
}

func (h *torrentsPrefsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/auth/login":
		if h.loginCookie != "" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: h.loginCookie})
		}
		_, _ = io.WriteString(w, "Ok.")
	case "/api/v2/torrents/info":
		_, _ = io.WriteString(w, h.infoBody)
	case "/api/v2/app/preferences":
		h.prefsCalls.Add(1)
		_, _ = io.WriteString(w, h.prefsBody)
	default:
		http.NotFound(w, r)
	}
}

func TestGetItems_MetaDLDhtDisabledPromotesToWarning(t *testing.T) {
	t.Parallel()
	h := &torrentsPrefsHandler{
		loginCookie: "sid",
		infoBody:    `[{"hash":"h","name":"MetaStuck","size":100,"progress":0,"state":"metaDL","category":"tv","save_path":"/dl","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`,
		prefsBody:   `{"save_path":"/dl","dht":false,"max_ratio_act":0}`,
	}
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
	if got[0].Status != downloadclients.DownloadItemStatusWarning {
		t.Errorf("Status = %v, want Warning (dht=false on metaDL)", got[0].Status)
	}
	if !strings.Contains(strings.ToLower(got[0].Message), "dht") {
		t.Errorf("Message = %q, want substring 'dht'", got[0].Message)
	}
	if h.prefsCalls.Load() != 1 {
		t.Errorf("prefs called %d times, want exactly 1 (lazy probe)", h.prefsCalls.Load())
	}
}

func TestGetItems_MetaDLDhtEnabledStaysQueued(t *testing.T) {
	t.Parallel()
	h := &torrentsPrefsHandler{
		loginCookie: "sid",
		infoBody:    `[{"hash":"h","name":"MetaOK","size":100,"progress":0,"state":"metaDL","category":"tv","save_path":"/dl","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`,
		prefsBody:   `{"save_path":"/dl","dht":true,"max_ratio_act":0}`,
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })

	got, _ := q.GetItems(context.Background())
	if len(got) != 1 || got[0].Status != downloadclients.DownloadItemStatusQueued {
		t.Errorf("Status = %v, want Queued (dht=true on metaDL)", got[0].Status)
	}
}

func TestGetItems_MetaDLPrefsProbeFiresOnceForMultipleTorrents(t *testing.T) {
	t.Parallel()
	// Two metaDL torrents — the lazy GetConfig probe should fire
	// exactly once across both.
	h := &torrentsPrefsHandler{
		loginCookie: "sid",
		infoBody:    `[{"hash":"a","name":"A","size":1,"progress":0,"state":"metaDL","category":"tv","save_path":"/dl","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2},{"hash":"b","name":"B","size":1,"progress":0,"state":"forcedMetaDL","category":"tv","save_path":"/dl","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`,
		prefsBody:   `{"save_path":"/dl","dht":false,"max_ratio_act":0}`,
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })

	got, _ := q.GetItems(context.Background())
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, item := range got {
		if item.Status != downloadclients.DownloadItemStatusWarning {
			t.Errorf("torrent %s: Status = %v, want Warning", item.DownloadID, item.Status)
		}
	}
	if h.prefsCalls.Load() != 1 {
		t.Errorf("prefs called %d times, want exactly 1 (lazy + cached across items)", h.prefsCalls.Load())
	}
}

func TestGetItems_NoMetaDLSkipsPrefsProbe(t *testing.T) {
	t.Parallel()
	// No torrents in metaDL → the lazy GetConfig probe should never fire.
	h := &torrentsPrefsHandler{
		loginCookie: "sid",
		infoBody:    `[{"hash":"h","name":"X","size":100,"progress":0.5,"state":"downloading","category":"tv","save_path":"/dl","content_path":"","ratio":0,"ratio_limit":-2,"seeding_time_limit":-2,"inactive_seeding_time_limit":-2}]`,
		prefsBody:   `{"save_path":"/dl","dht":false,"max_ratio_act":0}`,
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	q := mustNewQBit(t, srv, func(s *qbittorrent.Settings) { s.Username = "u"; s.Password = "p" })

	if _, err := q.GetItems(context.Background()); err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if h.prefsCalls.Load() != 0 {
		t.Errorf("prefs called %d times, want 0 (no metaDL torrent)", h.prefsCalls.Load())
	}
}
