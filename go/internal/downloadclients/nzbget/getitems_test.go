package nzbget_test

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
	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// ============================================================================
// Nzbget.GetItems() — slice 5.G3d. Ports C# Nzbget.GetItems +
// GetQueue + GetHistory at Nzbget.cs:54-200.
// ============================================================================

type rpcServer struct {
	statusBody  string
	queueBody   string
	historyBody string

	statusCalls  atomic.Int32
	queueCalls   atomic.Int32
	historyCalls atomic.Int32
}

func newRPCServer(t *testing.T) (*rpcServer, *httptest.Server) {
	h := &rpcServer{
		statusBody:  `{"DownloadRate":0,"DownloadPaused":false}`,
		queueBody:   `[]`,
		historyBody: `[]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &env)
		respond := func(result string) {
			_, _ = io.WriteString(w, `{"version":"1.1","result":`+result+`}`)
		}
		switch env.Method {
		case "status":
			h.statusCalls.Add(1)
			respond(h.statusBody)
		case "listgroups":
			h.queueCalls.Add(1)
			respond(h.queueBody)
		case "history":
			h.historyCalls.Add(1)
			respond(h.historyBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newNzbgetForGetItems(t *testing.T, srv *httptest.Server, mutate func(*nzbget.Settings)) *nzbget.Nzbget {
	t.Helper()
	s := settingsFor(t, srv)
	if mutate != nil {
		mutate(&s)
	}
	proxy, err := nzbget.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return nzbget.New(&downloadclients.DownloadClient{ID: 11, Name: "nzb"}, s, proxy, nil)
}

func TestGetItems_EmptyQueueAndHistory(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "" // no filter
	})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GetItems() = %v, want empty", got)
	}
	if h.statusCalls.Load() != 1 || h.queueCalls.Load() != 1 || h.historyCalls.Load() != 1 {
		t.Errorf("call counts (status=%d queue=%d history=%d), want all 1",
			h.statusCalls.Load(), h.queueCalls.Load(), h.historyCalls.Load())
	}
}

func TestGetItems_QueueDownloadingItem(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.queueBody = `[{
		"NZBID":42,"NZBName":"Show.S01E01.nzb","Category":"tv",
		"FileSizeLo":1073741824,"FileSizeHi":0,
		"RemainingSizeLo":536870912,"RemainingSizeHi":0,
		"PausedSizeLo":0,"PausedSizeHi":0,
		"ActiveDownloads":1,
		"Parameters":[{"Name":"drone","Value":"sonarr-grab-123"}]
	}]`
	h.statusBody = `{"DownloadRate":1048576,"DownloadPaused":false}`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems() err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	item := got[0]
	if item.DownloadID != "sonarr-grab-123" {
		t.Errorf("DownloadID = %q, want sonarr-grab-123 (drone param)", item.DownloadID)
	}
	if item.Title != "Show.S01E01.nzb" {
		t.Errorf("Title = %q", item.Title)
	}
	if item.TotalSize != 1073741824 {
		t.Errorf("TotalSize = %d", item.TotalSize)
	}
	if item.RemainingSize != 536870912 {
		t.Errorf("RemainingSize = %d", item.RemainingSize)
	}
	if item.Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status = %v, want Downloading", item.Status)
	}
	if item.RemainingTime == nil {
		t.Error("RemainingTime nil, want non-nil (DownloadRate set)")
	}
}

func TestGetItems_QueuePausedGlobally(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.statusBody = `{"DownloadRate":0,"DownloadPaused":true}`
	h.queueBody = `[{"NZBID":1,"NZBName":"A","Category":"tv","FileSizeLo":1000,"RemainingSizeLo":500,"ActiveDownloads":1,"Parameters":[]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].Status != downloadclients.DownloadItemStatusPaused {
		t.Errorf("Status = %v, want Paused (DownloadPaused=true)", got[0].Status)
	}
}

func TestGetItems_QueueAllPausedSize(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	// remaining == paused != 0 → Paused arm (per C# :80)
	h.queueBody = `[{"NZBID":1,"NZBName":"A","Category":"tv","FileSizeLo":1000,"RemainingSizeLo":500,"PausedSizeLo":500,"Parameters":[]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].Status != downloadclients.DownloadItemStatusPaused {
		t.Errorf("Status = %v, want Paused (remainingSize==pausedSize)", got[0].Status)
	}
}

func TestGetItems_QueueQueuedNoActiveDownloads(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	// ActiveDownloads=0 + remainingSize!=0 → Queued (C# :87-90)
	h.queueBody = `[{"NZBID":1,"NZBName":"A","Category":"tv","FileSizeLo":1000,"RemainingSizeLo":500,"PausedSizeLo":0,"ActiveDownloads":0,"Parameters":[]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].Status != downloadclients.DownloadItemStatusQueued {
		t.Errorf("Status = %v, want Queued", got[0].Status)
	}
}

func TestGetItems_HistorySuccess(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.historyBody = `[{
		"ID":99,"Name":"Show.S01E01","Category":"tv",
		"FileSizeLo":1073741824,"FileSizeHi":0,
		"ParStatus":"SUCCESS","UnpackStatus":"SUCCESS","MoveStatus":"SUCCESS","ScriptStatus":"SUCCESS","DeleteStatus":"NONE","MarkStatus":"NONE",
		"DestDir":"/dl/intermediate","FinalDir":"/dl/final/Show.S01E01",
		"Parameters":[{"Name":"drone","Value":"sonarr-grab-456"}]
	}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Status = %v, want Completed", got[0].Status)
	}
	if got[0].DownloadID != "sonarr-grab-456" {
		t.Errorf("DownloadID = %q", got[0].DownloadID)
	}
	// FinalDir wins over DestDir (C# :121).
	if got[0].OutputPath.String() != "/dl/final/Show.S01E01" {
		t.Errorf("OutputPath = %q, want FinalDir", got[0].OutputPath.String())
	}
}

func TestGetItems_HistoryFailureCascade(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		par        string
		unpack     string
		move       string
		script     string
		del        string
		mark       string
		wantStatus downloadclients.DownloadItemStatus
	}{
		{"par-failed", "FAILURE", "SUCCESS", "SUCCESS", "SUCCESS", "NONE", "NONE", downloadclients.DownloadItemStatusFailed},
		{"unpack-space", "SUCCESS", "SPACE", "SUCCESS", "SUCCESS", "NONE", "NONE", downloadclients.DownloadItemStatusWarning},
		{"unpack-failed", "SUCCESS", "FAILURE", "SUCCESS", "SUCCESS", "NONE", "NONE", downloadclients.DownloadItemStatusFailed},
		{"move-failed-warns", "SUCCESS", "SUCCESS", "FAILURE", "SUCCESS", "NONE", "NONE", downloadclients.DownloadItemStatusWarning},
		{"script-failed", "SUCCESS", "SUCCESS", "SUCCESS", "FAILURE", "NONE", "NONE", downloadclients.DownloadItemStatusFailed},
		{"delete-health-fails", "SUCCESS", "SUCCESS", "SUCCESS", "SUCCESS", "HEALTH", "NONE", downloadclients.DownloadItemStatusFailed},
		{"delete-other-warns", "SUCCESS", "SUCCESS", "SUCCESS", "SUCCESS", "AGEDELETED", "NONE", downloadclients.DownloadItemStatusWarning},
		{"manual-marked-bad", "SUCCESS", "SUCCESS", "SUCCESS", "SUCCESS", "MANUAL", "BAD", downloadclients.DownloadItemStatusFailed},
		{"manual-not-marked", "SUCCESS", "SUCCESS", "SUCCESS", "SUCCESS", "MANUAL", "NONE", downloadclients.DownloadItemStatusFailed}, // Go port keeps row as Failed vs C# `continue`
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, srv := newRPCServer(t)
			h.historyBody = `[{"ID":1,"Name":"X","Category":"tv","ParStatus":"` + tc.par +
				`","UnpackStatus":"` + tc.unpack + `","MoveStatus":"` + tc.move +
				`","ScriptStatus":"` + tc.script + `","DeleteStatus":"` + tc.del +
				`","MarkStatus":"` + tc.mark + `","DestDir":"/dl","Parameters":[]}]`
			n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
				s.TvCategory = ""
			})
			got, err := n.GetItems(context.Background())
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			if got[0].Status != tc.wantStatus {
				t.Errorf("[%s] Status = %v, want %v", tc.name, got[0].Status, tc.wantStatus)
			}
		})
	}
}

func TestGetItems_CategoryFilter(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	h.queueBody = `[
		{"NZBID":1,"NZBName":"Match","Category":"tv","FileSizeLo":100,"Parameters":[]},
		{"NZBID":2,"NZBName":"Skip","Category":"other","FileSizeLo":100,"Parameters":[]}
	]`
	h.historyBody = `[
		{"ID":3,"Name":"OtherHist","Category":"movies","ParStatus":"SUCCESS","UnpackStatus":"SUCCESS","MoveStatus":"SUCCESS","ScriptStatus":"SUCCESS","DeleteStatus":"NONE","MarkStatus":"NONE","Parameters":[]}
	]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = "tv"
	})
	got, err := n.GetItems(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0].Title != "Match" {
		t.Errorf("filtered items = %+v, want only Match (Category=tv)", got)
	}
}

func TestGetItems_DroneFallbackToNzbId(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	// No drone parameter — DownloadID falls back to "42".
	h.queueBody = `[{"NZBID":42,"NZBName":"A","Category":"tv","FileSizeLo":100,"Parameters":[]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].DownloadID != "42" {
		t.Errorf("DownloadID = %q, want 42 (fallback)", got[0].DownloadID)
	}
}

func TestGetItems_DroneIntegerValue(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	// Some legacy installs store drone as an int.
	h.queueBody = `[{"NZBID":1,"NZBName":"A","Category":"tv","FileSizeLo":100,"Parameters":[{"Name":"drone","Value":12345}]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].DownloadID != "12345" {
		t.Errorf("DownloadID = %q, want 12345 (int drone)", got[0].DownloadID)
	}
}

func TestGetItems_LoHiCombinedCorrectly(t *testing.T) {
	t.Parallel()
	h, srv := newRPCServer(t)
	// Hi=1 + Lo=0 → 2^32 = 4294967296 bytes
	h.queueBody = `[{"NZBID":1,"NZBName":"Big","Category":"tv","FileSizeLo":0,"FileSizeHi":1,"Parameters":[]}]`
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	got, _ := n.GetItems(context.Background())
	if got[0].TotalSize != 4294967296 {
		t.Errorf("TotalSize = %d, want 2^32 = 4294967296", got[0].TotalSize)
	}
}

func TestGetItems_NilProxyErrors(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	_, err := n.GetItems(context.Background())
	if err == nil {
		t.Fatal("GetItems(nil proxy) err = nil, want error")
	}
}

func TestGetItems_AuthFailureBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	n := newNzbgetForGetItems(t, srv, func(s *nzbget.Settings) {
		s.TvCategory = ""
	})
	_, err := n.GetItems(context.Background())
	if !errors.Is(err, nzbget.ErrAuthFailed) {
		t.Errorf("GetItems() err = %v, want ErrAuthFailed", err)
	}
}
