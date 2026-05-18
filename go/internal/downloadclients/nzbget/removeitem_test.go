package nzbget_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
)

// ============================================================================
// Nzbget.RemoveItem() — slice 5.G3f. Ports C# Nzbget.RemoveItem at
// Nzbget.cs:202-210 + NzbgetProxy.RemoveItem at :165-202.
// ============================================================================

// removeServer captures editqueue calls + serves queue + history
// per the configured bodies.
type removeServer struct {
	queueBody     string
	historyBody   string
	editResult    atomic.Int32 // 0 = default true; -1 = false
	editErrorBody string

	editCalls    atomic.Int32
	editParams   atomic.Value // []any
	queueCalls   atomic.Int32
	historyCalls atomic.Int32
}

func newRemoveServer(t *testing.T) (*removeServer, *httptest.Server) {
	h := &removeServer{
		queueBody:   `[]`,
		historyBody: `[]`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			ID     int             `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		switch env.Method {
		case "listgroups":
			h.queueCalls.Add(1)
			_, _ = io.WriteString(w, `{"version":"1.1","result":`+h.queueBody+`}`)
		case "history":
			h.historyCalls.Add(1)
			_, _ = io.WriteString(w, `{"version":"1.1","result":`+h.historyBody+`}`)
		case "editqueue":
			h.editCalls.Add(1)
			var p []any
			_ = json.Unmarshal(env.Params, &p)
			h.editParams.Store(p)
			if h.editErrorBody != "" {
				_, _ = io.WriteString(w, `{"version":"1.1","error":{"name":"E","code":1,"message":"`+h.editErrorBody+`"}}`)
				return
			}
			r := h.editResult.Load()
			if r == -1 {
				_, _ = io.WriteString(w, `{"version":"1.1","result":false}`)
				return
			}
			_, _ = io.WriteString(w, `{"version":"1.1","result":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return h, srv
}

func newNzbgetForRemove(t *testing.T, srv *httptest.Server) *nzbget.Nzbget {
	t.Helper()
	s := settingsFor(t, srv)
	proxy, err := nzbget.NewProxy(srv.Client(), nil)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	return nzbget.New(nil, s, proxy, nil)
}

func TestRemoveItem_NilItem(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	if err := n.RemoveItem(context.Background(), nil, false); err == nil {
		t.Fatal("RemoveItem(nil) err = nil, want error")
	}
}

func TestRemoveItem_NilProxy(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	err := n.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "abc"}, false)
	if err == nil {
		t.Fatal("RemoveItem(nil proxy) err = nil, want error")
	}
}

func TestRemoveItem_QueueMatchByDrone(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.queueBody = `[{"NZBID":42,"NZBName":"Show.S01E01","Parameters":[{"Name":"drone","Value":"abcdef1234567890abcdef1234567890"}]}]`
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abcdef1234567890abcdef1234567890"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	if h.editCalls.Load() != 1 {
		t.Errorf("editqueue fired %d times, want 1", h.editCalls.Load())
	}
	params, _ := h.editParams.Load().([]any)
	if len(params) != 4 {
		t.Fatalf("params = %v, want 4", params)
	}
	if params[0] != "GroupFinalDelete" {
		t.Errorf("params[0] = %v, want GroupFinalDelete", params[0])
	}
	if params[3] != float64(42) {
		t.Errorf("params[3] = %v, want 42 (queueItem.NzbId)", params[3])
	}
}

func TestRemoveItem_HistoryMatchByDrone(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.historyBody = `[{"ID":99,"Name":"Old.Show","Parameters":[{"Name":"drone","Value":"deadbeefdeadbeefdeadbeefdeadbeef"}]}]`
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "deadbeefdeadbeefdeadbeefdeadbeef"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	if h.editCalls.Load() != 1 {
		t.Errorf("editqueue fired %d times, want 1", h.editCalls.Load())
	}
	params, _ := h.editParams.Load().([]any)
	if params[0] != "HistoryDelete" {
		t.Errorf("params[0] = %v, want HistoryDelete", params[0])
	}
	if params[3] != float64(99) {
		t.Errorf("params[3] = %v, want 99 (historyItem.Id)", params[3])
	}
}

func TestRemoveItem_QueueMatchByNumericNzbId(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.queueBody = `[{"NZBID":7,"NZBName":"Legacy","Parameters":[]}]`
	n := newNzbgetForRemove(t, srv)
	// Numeric < 10 chars + no drone-GUID — match by NzbId.
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "7"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	params, _ := h.editParams.Load().([]any)
	if params[0] != "GroupFinalDelete" {
		t.Errorf("params[0] = %v, want GroupFinalDelete", params[0])
	}
	if params[3] != float64(7) {
		t.Errorf("params[3] = %v, want 7", params[3])
	}
}

func TestRemoveItem_HistoryMatchByNumericId(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.historyBody = `[{"ID":11,"Name":"LegacyHist","Parameters":[]}]`
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "11"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v", err)
	}
	params, _ := h.editParams.Load().([]any)
	if params[0] != "HistoryDelete" {
		t.Errorf("params[0] = %v, want HistoryDelete", params[0])
	}
}

func TestRemoveItem_NotFoundReturnsNil(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	// Empty queue + history → not found anywhere.
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "unknownDroneID0000000000000000000"}, false)
	if err != nil {
		t.Fatalf("RemoveItem() err = %v, want nil (warn-only)", err)
	}
	if h.editCalls.Load() != 0 {
		t.Errorf("editqueue fired %d times, want 0 (unknown ID skips dispatch)", h.editCalls.Load())
	}
}

func TestRemoveItem_EditQueueReturnsFalseWarns(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.queueBody = `[{"NZBID":1,"NZBName":"X","Parameters":[{"Name":"drone","Value":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]`
	h.editResult.Store(-1) // return false
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, false)
	if err != nil {
		t.Errorf("RemoveItem() err = %v, want nil (false editqueue is warn-only)", err)
	}
}

func TestRemoveItem_EditQueueRPCErrorWarns(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.queueBody = `[{"NZBID":1,"NZBName":"X","Parameters":[{"Name":"drone","Value":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}]`
	h.editErrorBody = "internal"
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, false)
	if err != nil {
		t.Errorf("RemoveItem() err = %v, want nil (editqueue RPC error is warn-only)", err)
	}
}

func TestRemoveItem_AuthFailureDuringQueueFetchBubbles(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "abc"}, false)
	if err == nil {
		t.Fatal("RemoveItem(401) err = nil, want auth failure bubble")
	}
}

func TestRemoveItem_DeleteDataLogsWarnButProceeds(t *testing.T) {
	t.Parallel()
	h, srv := newRemoveServer(t)
	h.queueBody = `[{"NZBID":1,"NZBName":"X","Parameters":[{"Name":"drone","Value":"cccccccccccccccccccccccccccccccc"}]}]`
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "cccccccccccccccccccccccccccccccc"}, true)
	if err != nil {
		t.Fatalf("RemoveItem(deleteData=true) err = %v", err)
	}
	if h.editCalls.Load() != 1 {
		t.Errorf("editqueue fired %d times, want 1 (deleteData warning doesn't block dispatch)", h.editCalls.Load())
	}
}

func TestRemoveItem_NumericArmDoesNotFallThroughToDroneArm(t *testing.T) {
	t.Parallel()
	// DownloadID="42" parses as int and is < 10 chars → numeric
	// arm is selected. It matches against NzbId/Id only; the C#
	// tree at NzbgetProxy.cs:170-186 does NOT fall through to the
	// drone-parameter arm. So a numeric DownloadID that doesn't
	// match any NzbId/Id (even if some torrent has a drone
	// parameter with that string) is "not found".
	h, srv := newRemoveServer(t)
	// NzbId=1, drone value="42" — the numeric arm checks NzbId==42
	// (no match) and bails; drone match is not attempted.
	h.queueBody = `[{"NZBID":1,"NZBName":"X","Parameters":[{"Name":"drone","Value":"42"}]}]`
	n := newNzbgetForRemove(t, srv)
	err := n.RemoveItem(context.Background(),
		&downloadclients.Item{DownloadID: "42"}, false)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if h.editCalls.Load() != 0 {
		t.Errorf("editqueue fired %d times, want 0 (numeric arm doesn't fall through to drone)", h.editCalls.Load())
	}
}
