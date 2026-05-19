package qbittorrent_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // BitTorrent info-hash spec uses SHA-1.
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
)

// ============================================================================
// QBittorrent.Download() — slice 5.G1e. Ports C# QBittorrent
// AddFromMagnetLink + AddFromTorrentFile at QBittorrent.cs:75-216
// + QBittorrentProxyV2.AddTorrentFromUrl/AddTorrentFromFile at :139-181.
// ============================================================================

// addServer is the canonical handler covering login + version +
// /api/v2/torrents/add + force-start + share-limits. Test cases pin
// the captured fields via the per-server atomic.Value handles.
type addServer struct {
	versionBody string

	addCalls         atomic.Int32
	forceStartCalls  atomic.Int32
	shareLimitCalls  atomic.Int32
	shareLimitStatus atomic.Int32 // 200 by default; tests override (e.g. 404 for older API)
	addResponseBody  string
	addStatus        int

	addedFormFields map[string]string
	addedTorrents   [][]byte
	addedURL        atomic.Value // string
	forceStartForm  atomic.Value // url.Values
	shareLimitForm  atomic.Value // url.Values
}

func newAddServer(t *testing.T) (*addServer, *httptest.Server) {
	h := &addServer{
		versionBody:     "v4.5.0",
		addedFormFields: map[string]string{},
		addResponseBody: "Ok.",
		addStatus:       http.StatusOK,
	}
	h.shareLimitStatus.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")

		case "/api/v2/app/version":
			_, _ = io.WriteString(w, h.versionBody)

		case "/api/v2/torrents/add":
			h.addCalls.Add(1)
			ct := r.Header.Get("Content-Type")
			mediaType, params, err := mime.ParseMediaType(ct)
			if err != nil || !strings.HasPrefix(mediaType, "multipart/form-data") {
				t.Errorf("add Content-Type = %q, want multipart/form-data", ct)
			} else {
				mr := multipart.NewReader(r.Body, params["boundary"])
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					data, _ := io.ReadAll(part)
					switch part.FormName() {
					case "torrents":
						h.addedTorrents = append(h.addedTorrents, data)
					case "urls":
						h.addedURL.Store(string(data))
					default:
						h.addedFormFields[part.FormName()] = string(data)
					}
				}
			}
			w.WriteHeader(h.addStatus)
			_, _ = io.WriteString(w, h.addResponseBody)

		case "/api/v2/torrents/setForceStart":
			h.forceStartCalls.Add(1)
			_ = r.ParseForm()
			h.forceStartForm.Store(r.PostForm)
			w.WriteHeader(http.StatusOK)

		case "/api/v2/torrents/setShareLimits":
			h.shareLimitCalls.Add(1)
			_ = r.ParseForm()
			h.shareLimitForm.Store(r.PostForm)
			w.WriteHeader(int(h.shareLimitStatus.Load()))

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newQBitForDownload(t *testing.T, srv *httptest.Server, mutate func(*qbittorrent.Settings)) *qbittorrent.QBittorrent {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := qbittorrent.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return qbittorrent.New(&downloadclients.DownloadClient{ID: 7, Name: "qb"}, s, proxy, nil)
}

func TestDownload_NilRequest(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.Download(context.Background(), nil)
	if err == nil {
		t.Fatal("Download(nil) err = nil, want error")
	}
}

func TestDownload_NoSourceErrors(t *testing.T) {
	t.Parallel()
	_, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		InfoHash: "abc123",
	})
	if err == nil {
		t.Fatal("Download(no source) err = nil, want error")
	}
}

func TestDownload_Magnet(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.TvCategory = "tv-sonarr"
	})

	hash, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		MagnetURL:    "magnet:?xt=urn:btih:DEADBEEFCAFEBABEDEADBEEFCAFEBABEDEADBEEF",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if hash != "DEADBEEFCAFEBABEDEADBEEFCAFEBABEDEADBEEF" {
		t.Errorf("Download() = %q, want upper-cased hash", hash)
	}
	if got := h.addedURL.Load(); got != "magnet:?xt=urn:btih:DEADBEEFCAFEBABEDEADBEEFCAFEBABEDEADBEEF" {
		t.Errorf("urls field = %q", got)
	}
	if got := h.addedFormFields["category"]; got != "tv-sonarr" {
		t.Errorf("category field = %q, want tv-sonarr", got)
	}
	if h.forceStartCalls.Load() != 0 {
		t.Errorf("force-start fired despite InitialState=Start")
	}
	if h.shareLimitCalls.Load() != 0 {
		t.Errorf("share-limits fired despite nil seed config")
	}
}

func TestDownload_URL(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		DownloadURL:  "https://indexer.example/download/123.torrent",
		InfoHash:     "abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if got := h.addedURL.Load(); got != "https://indexer.example/download/123.torrent" {
		t.Errorf("urls field = %q", got)
	}
}

func TestDownload_TorrentBytes(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	payload := []byte("d4:infod4:name3:abceee")
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  payload,
		InfoHash:     "abc",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if len(h.addedTorrents) != 1 {
		t.Fatalf("len(addedTorrents) = %d, want 1", len(h.addedTorrents))
	}
	if !equalBytes(h.addedTorrents[0], payload) {
		t.Errorf("torrent bytes mismatch")
	}
}

func TestDownload_InfoHashFromMagnet(t *testing.T) {
	t.Parallel()
	_, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	hash, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&dn=Foo&tr=udp://t.example",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if hash != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Errorf("Download() = %q, want hash from urn:btih:", hash)
	}
}

func TestDownload_NoInfoHashTorrentBytesErrors(t *testing.T) {
	t.Parallel()
	_, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  []byte("payload"),
	})
	if err == nil {
		t.Fatal("Download(torrent-bytes no InfoHash) err = nil, want error")
	}
}

func TestDownload_InfoHashExtractedFromBencode(t *testing.T) {
	t.Parallel()
	// Slice 6.H: real bencode .torrent body, no explicit InfoHash,
	// no Magnet — the resolver should extract the BEP-3 SHA-1 from
	// the info dict.
	infoDict := "d4:name5:helloe"
	body := []byte("d4:info" + infoDict + "e")

	// Compute expected hash inline so the test is self-consistent.
	sum := sha1Sum([]byte(infoDict))

	_, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	hash, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		TorrentBody:  body,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hash != strings.ToUpper(sum) {
		t.Errorf("Download() = %q, want %q (SHA-1 of info dict)", hash, strings.ToUpper(sum))
	}
}

func TestDownload_FailsResponseBody(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	h.addResponseBody = "Fails."
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if !errors.Is(err, qbittorrent.ErrAddFailed) {
		t.Errorf("Download() err = %v, want ErrAddFailed", err)
	}
}

func TestDownload_InitialStateStopUsesPaused(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.InitialState = qbittorrent.InitialStateStop
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if got, want := h.addedFormFields["paused"], "true"; got != want {
		t.Errorf("paused field = %q, want %q", got, want)
	}
	if _, present := h.addedFormFields["stopped"]; present {
		t.Errorf("stopped field present at qBittorrent < 5; should use 'paused'")
	}
}

func TestDownload_InitialStateStopV5UsesStopped(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	h.versionBody = "v5.0.0"
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.InitialState = qbittorrent.InitialStateStop
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if got, want := h.addedFormFields["stopped"], "true"; got != want {
		t.Errorf("stopped field = %q, want %q (qBittorrent >= 5)", got, want)
	}
	if _, present := h.addedFormFields["paused"]; present {
		t.Errorf("paused field present at qBittorrent >= 5; should use 'stopped'")
	}
}

func TestDownload_InitialStateForceStartFiresSetForceStart(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.InitialState = qbittorrent.InitialStateForceStart
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.forceStartCalls.Load() != 1 {
		t.Errorf("force-start fired %d times, want 1", h.forceStartCalls.Load())
	}
	form, _ := h.forceStartForm.Load().(url.Values)
	if form == nil {
		t.Fatal("force-start form missing")
	}
	if got := form.Get("hashes"); got != "cafe" {
		t.Errorf("force-start hashes = %q, want cafe", got)
	}
	if got := form.Get("value"); got != "true" {
		t.Errorf("force-start value = %q, want true", got)
	}
}

func TestDownload_SeedConfigAppliesShareLimits(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})

	ratio := 1.5
	seedTime := 90 * time.Minute
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
		SeedConfiguration: &downloadclients.TorrentSeedConfiguration{
			Ratio:    &ratio,
			SeedTime: &seedTime,
		},
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if h.shareLimitCalls.Load() != 1 {
		t.Errorf("setShareLimits fired %d times, want 1", h.shareLimitCalls.Load())
	}
	form, _ := h.shareLimitForm.Load().(url.Values)
	if form == nil {
		t.Fatal("setShareLimits form missing")
	}
	if got := form.Get("ratioLimit"); got != "1.5" {
		t.Errorf("ratioLimit = %q, want 1.5", got)
	}
	if got := form.Get("seedingTimeLimit"); got != "90" {
		t.Errorf("seedingTimeLimit = %q, want 90 (minutes)", got)
	}
}

func TestDownload_SetShareLimits404Tolerated(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	h.shareLimitStatus.Store(http.StatusNotFound)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
	})
	ratio := 2.0
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
		SeedConfiguration: &downloadclients.TorrentSeedConfiguration{
			Ratio: &ratio,
		},
	})
	if err != nil {
		t.Fatalf("Download() err = %v (404 on setShareLimits should be tolerated)", err)
	}
}

func TestDownload_ContentLayoutOriginalSubfolder(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		setting int
		want    string
	}{
		{qbittorrent.ContentLayoutOriginal, "Original"},
		{qbittorrent.ContentLayoutSubfolder, "Subfolder"},
	} {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			h, srv := newAddServer(t)
			q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
				s.ApiKey = "k"
				s.ContentLayout = tc.setting
			})
			_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
				MagnetURL: "magnet:?xt=urn:btih:cafe",
			})
			if err != nil {
				t.Fatalf("Download() err = %v", err)
			}
			if got := h.addedFormFields["contentLayout"]; got != tc.want {
				t.Errorf("contentLayout = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownload_ContentLayoutDefaultSkipsField(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		// ContentLayoutDefault is 0 — default zero-value.
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if _, present := h.addedFormFields["contentLayout"]; present {
		t.Errorf("contentLayout field set on ContentLayoutDefault")
	}
}

func TestDownload_SequentialAndFirstLast(t *testing.T) {
	t.Parallel()
	h, srv := newAddServer(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.ApiKey = "k"
		s.SequentialOrder = true
		s.FirstAndLast = true
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		MagnetURL: "magnet:?xt=urn:btih:cafe",
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if got, want := h.addedFormFields["sequentialDownload"], "true"; got != want {
		t.Errorf("sequentialDownload = %q, want %q", got, want)
	}
	if got, want := h.addedFormFields["firstLastPiecePrio"], "true"; got != want {
		t.Errorf("firstLastPiecePrio = %q, want %q", got, want)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ============================================================================
// Slice 6.F: MoveTorrentToTopInQueue post-add wiring. Applies when the
// per-recency priority setting resolves to PriorityFirst.
// ============================================================================

// addServerWithPrio extends the canonical addServer wiring with a
// /api/v2/torrents/topPrio handler so the slice-6.F post-add RPC is
// observable. Returns 200 by default; tests can override
// topPrioStatus to exercise the 409/error arms.
type addServerWithPrio struct {
	addServer
	topPrioCalls  atomic.Int32
	topPrioStatus atomic.Int32 // 0 → 200, set to override
	topPrioForm   atomic.Value // url.Values
}

func newAddServerWithPrio(t *testing.T) (*addServerWithPrio, *httptest.Server) {
	h := &addServerWithPrio{
		addServer: addServer{
			versionBody:     "v4.5.0",
			addedFormFields: map[string]string{},
			addResponseBody: "Ok.",
			addStatus:       http.StatusOK,
		},
	}
	h.shareLimitStatus.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/torrents/topPrio" {
			h.topPrioCalls.Add(1)
			_ = r.ParseForm()
			h.topPrioForm.Store(r.PostForm)
			s := int(h.topPrioStatus.Load())
			if s == 0 {
				s = http.StatusOK
			}
			w.WriteHeader(s)
			return
		}
		// Delegate to the shared addServer plumbing for the
		// remaining endpoints.
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, h.versionBody)
		case "/api/v2/torrents/add":
			h.addCalls.Add(1)
			w.WriteHeader(h.addStatus)
			_, _ = io.WriteString(w, h.addResponseBody)
		case "/api/v2/torrents/setForceStart":
			h.forceStartCalls.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/api/v2/torrents/setShareLimits":
			h.shareLimitCalls.Add(1)
			w.WriteHeader(int(h.shareLimitStatus.Load()))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func TestDownload_RecentTvPriorityFirstFiresMoveToTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServerWithPrio(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.RecentTvPriority = qbittorrent.PriorityFirst
		s.OlderTvPriority = qbittorrent.PriorityLast
	})
	hash, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		MagnetURL:    "magnet:?xt=urn:btih:DEADBEEF",
		IsRecent:     true,
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hash != "DEADBEEF" {
		t.Errorf("hash = %q, want DEADBEEF", hash)
	}
	if h.topPrioCalls.Load() != 1 {
		t.Errorf("topPrio fired %d times, want 1 (RecentTvPriority=First + IsRecent=true)", h.topPrioCalls.Load())
	}
	form, _ := h.topPrioForm.Load().(url.Values)
	if got := form.Get("hashes"); got != "deadbeef" {
		t.Errorf("topPrio hashes = %q, want lowercase deadbeef", got)
	}
}

func TestDownload_OlderTvPriorityLastSkipsMoveToTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServerWithPrio(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.RecentTvPriority = qbittorrent.PriorityFirst
		s.OlderTvPriority = qbittorrent.PriorityLast
	})
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E99",
		MagnetURL:    "magnet:?xt=urn:btih:CAFEFEED",
		IsRecent:     false, // IsRecent=false → OlderTvPriority=Last → no topPrio
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if h.topPrioCalls.Load() != 0 {
		t.Errorf("topPrio fired %d times, want 0 (OlderTvPriority=Last)", h.topPrioCalls.Load())
	}
}

func TestDownload_TopPrio409QueueingDisabledIsSilent(t *testing.T) {
	t.Parallel()
	h, srv := newAddServerWithPrio(t)
	h.topPrioStatus.Store(http.StatusConflict)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.RecentTvPriority = qbittorrent.PriorityFirst
	})
	// 409 from qBittorrent's queueing-disabled state is best-effort;
	// the proxy swallows it and Download returns success.
	if _, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		MagnetURL:    "magnet:?xt=urn:btih:DEADBEEF",
		IsRecent:     true,
	}); err != nil {
		t.Errorf("Download must not fail on topPrio 409 (queueing disabled): got %v", err)
	}
	if h.topPrioCalls.Load() != 1 {
		t.Errorf("topPrio still fires once even on 409 (proxy must attempt + swallow); got %d", h.topPrioCalls.Load())
	}
}

func TestDownload_RecentTvPriorityLastSkipsMoveToTop(t *testing.T) {
	t.Parallel()
	h, srv := newAddServerWithPrio(t)
	q := newQBitForDownload(t, srv, func(s *qbittorrent.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.RecentTvPriority = qbittorrent.PriorityLast
		s.OlderTvPriority = qbittorrent.PriorityFirst
	})
	if _, err := q.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Recent",
		MagnetURL:    "magnet:?xt=urn:btih:DEADBEEF",
		IsRecent:     true, // IsRecent=true → RecentTvPriority=Last → no topPrio
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if h.topPrioCalls.Load() != 0 {
		t.Errorf("topPrio fired %d times, want 0 (RecentTvPriority=Last)", h.topPrioCalls.Load())
	}
}

// sha1Sum returns the lowercase-hex SHA-1 — used by the bencode
// info-hash extraction test (slice 6.H).
func sha1Sum(b []byte) string {
	sum := sha1.Sum(b) //nolint:gosec // BEP-3 info-hash spec.
	return hex.EncodeToString(sum[:])
}
