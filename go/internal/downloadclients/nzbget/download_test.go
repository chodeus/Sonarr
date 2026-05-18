package nzbget_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// ============================================================================
// Nzbget.Download() — slice 5.G3e. Ports C# Nzbget.AddFromNzbFile at
// Nzbget.cs:39-52 + NzbgetProxy.DownloadNzb v16 arm at :55-67.
// ============================================================================

// appendServer captures the append RPC params for inspection. The
// `appendResult` int is the value the server reports back as the
// NzbId — positive=success, 0/negative=failure.
type appendServer struct {
	appendCalls  atomic.Int32
	appendParams atomic.Value // []any
	appendResult atomic.Int64 // 0 = default success (returns 100)
}

func newAppendServer(t *testing.T) (*appendServer, *httptest.Server) {
	h := &appendServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		if env.Method != "append" {
			t.Errorf("unexpected method %q", env.Method)
			http.NotFound(w, r)
			return
		}
		h.appendCalls.Add(1)
		var p []any
		_ = json.Unmarshal(env.Params, &p)
		h.appendParams.Store(p)
		result := int(h.appendResult.Load())
		if result == 0 {
			result = 100 // default success NzbId
		}
		_, _ = io.WriteString(w, `{"version":"1.1","result":`+intStr(result)+`}`)
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func intStr(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func newNzbgetForDownload(t *testing.T, srv *httptest.Server, mutate func(*nzbget.Settings)) *nzbget.Nzbget {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := nzbget.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return nzbget.New(&downloadclients.DownloadClient{ID: 5, Name: "nzb"}, s, proxy, nil)
}

var droneIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestDownload_NilRequest(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	if _, err := n.Download(context.Background(), nil); err == nil {
		t.Fatal("Download(nil) err = nil, want error")
	}
}

func TestDownload_NilProxy(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	_, err := n.Download(context.Background(),
		&downloadclients.GrabRequest{NzbBody: []byte("data")})
	if err == nil {
		t.Fatal("Download(nil proxy) err = nil, want error")
	}
}

func TestDownload_EmptyNzbBodyErrors(t *testing.T) {
	t.Parallel()
	_, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		DownloadURL:  "https://indexer/dl.nzb",
		// NzbBody empty
	})
	if err == nil {
		t.Fatal("Download(empty NzbBody) err = nil, want error")
	}
}

func TestDownload_HappyPath(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)

	payload := []byte("<nzb><file>...</file></nzb>")
	droneID, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01",
		NzbBody:      payload,
	})
	if err != nil {
		t.Fatalf("Download() err = %v", err)
	}
	if !droneIDPattern.MatchString(droneID) {
		t.Errorf("droneID = %q, want 32-char hex string", droneID)
	}
	if h.appendCalls.Load() != 1 {
		t.Errorf("append fired %d times, want 1", h.appendCalls.Load())
	}

	params, _ := h.appendParams.Load().([]any)
	if len(params) != 10 {
		t.Fatalf("params = %v, want 10 positional args", params)
	}
	// [0] = NZBFilename
	if params[0] != "Show.S01E01.nzb" {
		t.Errorf("filename = %v, want Show.S01E01.nzb", params[0])
	}
	// [1] = base64-encoded NZB
	encoded, _ := params[1].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded payload mismatch: %q vs %q", decoded, payload)
	}
	// [2] = Category
	if params[2] != "tv" {
		t.Errorf("category = %v, want tv", params[2])
	}
	// [3] = Priority (Normal=0 default)
	if params[3] != float64(0) {
		t.Errorf("priority = %v, want 0 (Normal)", params[3])
	}
	// [4] = AddToTop = false
	if params[4] != false {
		t.Errorf("addToTop = %v, want false", params[4])
	}
	// [5] = AddPaused = false (default)
	if params[5] != false {
		t.Errorf("addPaused = %v, want false", params[5])
	}
	// [9] = PP[] params array: ["drone", <id>]
	pp, ok := params[9].([]any)
	if !ok || len(pp) != 2 {
		t.Fatalf("PP[] = %v, want 2-element array", params[9])
	}
	if pp[0] != "drone" {
		t.Errorf("PP[0] = %v, want drone", pp[0])
	}
	if pp[1] != droneID {
		t.Errorf("PP[1] = %v, want droneID %q", pp[1], droneID)
	}
}

func TestDownload_ReleaseTitleAlreadyHasNzbSuffix(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01.nzb", // already has the suffix
		NzbBody:      []byte("data"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[0] != "Show.S01E01.nzb" {
		t.Errorf("filename = %v, want Show.S01E01.nzb (no double-suffix)", params[0])
	}
}

func TestDownload_EmptyReleaseTitle(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		NzbBody: []byte("data"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[0] != "release.nzb" {
		t.Errorf("filename = %v, want fallback release.nzb", params[0])
	}
}

func TestDownload_RecentPriorityUsedWhenIsRecent(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, func(s *nzbget.Settings) {
		s.RecentTvPriority = nzbget.PriorityHigh   // 50
		s.OlderTvPriority = nzbget.PriorityVeryLow // -100
	})
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
		IsRecent:     true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[3] != float64(nzbget.PriorityHigh) {
		t.Errorf("priority = %v, want %d (RecentTvPriority=High)", params[3], nzbget.PriorityHigh)
	}
}

func TestDownload_OlderPriorityUsedWhenNotRecent(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, func(s *nzbget.Settings) {
		s.RecentTvPriority = nzbget.PriorityHigh
		s.OlderTvPriority = nzbget.PriorityVeryLow
	})
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
		IsRecent:     false,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[3] != float64(nzbget.PriorityVeryLow) {
		t.Errorf("priority = %v, want %d (OlderTvPriority=VeryLow)", params[3], nzbget.PriorityVeryLow)
	}
}

func TestDownload_AddPausedWired(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, func(s *nzbget.Settings) {
		s.AddPaused = true
	})
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[5] != true {
		t.Errorf("addPaused = %v, want true", params[5])
	}
}

func TestDownload_CategoryFromSettings(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "sonarr-tv"
	})
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if params[2] != "sonarr-tv" {
		t.Errorf("category = %v, want sonarr-tv", params[2])
	}
}

func TestDownload_ZeroResultSurfacesErrAddFailed(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	h.appendResult.Store(0) // result=0 wouldn't be returned by default arm; force via skipping the default
	// Override the server to actually return 0 instead of the
	// default-100 fallback.
	srv.Close()
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"1.1","result":0}`)
	}))
	defer srv.Close()
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
	})
	if !errors.Is(err, nzbget.ErrAddFailed) {
		t.Errorf("err = %v, want ErrAddFailed", err)
	}
}

func TestDownload_NegativeResultSurfacesErrAddFailed(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	h.appendResult.Store(-1)
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
	})
	if !errors.Is(err, nzbget.ErrAddFailed) {
		t.Errorf("err = %v, want ErrAddFailed", err)
	}
}

func TestDownload_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "X",
		NzbBody:      []byte("d"),
	})
	if !errors.Is(err, nzbget.ErrAuthFailed) {
		t.Errorf("err = %v, want ErrAuthFailed", err)
	}
}

func TestDownload_DroneIDIsUnique(t *testing.T) {
	t.Parallel()
	_, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)

	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		id, err := n.Download(context.Background(), &downloadclients.GrabRequest{
			ReleaseTitle: "X",
			NzbBody:      []byte("d"),
		})
		if err != nil {
			t.Fatalf("Download()#%d err = %v", i, err)
		}
		if seen[id] {
			t.Errorf("droneID collision after %d calls: %q", i+1, id)
		}
		seen[id] = true
		if !droneIDPattern.MatchString(id) {
			t.Errorf("droneID = %q, want 32-char hex", id)
		}
	}
}

// Compile-time guard the suffix-add path doesn't double-suffix
// case variations.
func TestDownload_CaseInsensitiveSuffixCheck(t *testing.T) {
	t.Parallel()
	h, srv := newAppendServer(t)
	n := newNzbgetForDownload(t, srv, nil)
	_, err := n.Download(context.Background(), &downloadclients.GrabRequest{
		ReleaseTitle: "Show.S01E01.NZB", // uppercase suffix
		NzbBody:      []byte("d"),
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	params, _ := h.appendParams.Load().([]any)
	if !strings.EqualFold(params[0].(string), "show.s01e01.nzb") {
		t.Errorf("filename = %v, want case-insensitive .nzb suffix match (no double-suffix)", params[0])
	}
}
