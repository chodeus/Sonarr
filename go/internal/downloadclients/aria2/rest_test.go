package aria2_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/aria2"
	"sonarr.tv/sonarr/internal/ospath"
)

// stagedServer dispatches per-method responses so a single test can
// exercise the 3-call tellActive/tellWaiting/tellStopped fanout.
type stagedServer struct {
	srv      *httptest.Server
	bodiesMu sync.Mutex
	byMethod map[string]string
	calls    map[string]int
	captured atomic.Value // last body string
}

func newStagedServer(t *testing.T) *stagedServer {
	t.Helper()
	s := &stagedServer{
		byMethod: map[string]string{},
		calls:    map[string]int{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		s.captured.Store(string(body))
		method := extractMethodName(string(body))
		s.bodiesMu.Lock()
		s.calls[method]++
		resp, ok := s.byMethod[method]
		s.bodiesMu.Unlock()
		if !ok {
			// Default: empty array result.
			resp = `<?xml version="1.0"?><methodResponse><params><param><value><array><data></data></array></value></param></params></methodResponse>`
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stagedServer) set(method, body string) {
	s.bodiesMu.Lock()
	defer s.bodiesMu.Unlock()
	s.byMethod[method] = body
}

func (s *stagedServer) settings() aria2.Settings {
	u, _ := url.Parse(s.srv.URL)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n := 0
		for _, r := range p {
			n = n*10 + int(r-'0')
		}
		port = n
	}
	st := aria2.NewSettings()
	st.Host = u.Hostname()
	st.Port = port
	st.RpcPath = "/rpc"
	return st
}

func extractMethodName(body string) string {
	if i := strings.Index(body, "<methodName>"); i >= 0 {
		rest := body[i+len("<methodName>"):]
		if j := strings.Index(rest, "</methodName>"); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

// tellResponse builds an array-of-structs response body for one
// tellActive/Waiting/Stopped call.
func tellResponse(rows ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><methodResponse><params><param><value><array><data>`)
	for _, r := range rows {
		b.WriteString(`<value>`)
		b.WriteString(r)
		b.WriteString(`</value>`)
	}
	b.WriteString(`</data></array></value></param></params></methodResponse>`)
	return b.String()
}

// torrentStruct constructs an aria2 torrent struct with the fields
// the GetItems decoder reads.
type torrent struct {
	gid, infoHash, status, name, errorMsg     string
	total, completed, uploaded, downloadSpeed int64
	files                                     []string
}

func (t torrent) marshal() string {
	var b strings.Builder
	b.WriteString(`<struct>`)
	member(&b, "gid", str(t.gid))
	member(&b, "infoHash", str(t.infoHash))
	member(&b, "status", str(t.status))
	member(&b, "totalLength", str(intStr(t.total)))
	member(&b, "completedLength", str(intStr(t.completed)))
	member(&b, "uploadLength", str(intStr(t.uploaded)))
	member(&b, "downloadSpeed", str(intStr(t.downloadSpeed)))
	if t.errorMsg != "" {
		member(&b, "errorMessage", str(t.errorMsg))
	}
	if t.name != "" {
		// bittorrent.info.name nested struct.
		var info strings.Builder
		info.WriteString(`<struct>`)
		member(&info, "name", str(t.name))
		info.WriteString(`</struct>`)
		var bt strings.Builder
		bt.WriteString(`<struct>`)
		member(&bt, "info", info.String())
		bt.WriteString(`</struct>`)
		member(&b, "bittorrent", bt.String())
	}
	if len(t.files) > 0 {
		var arr strings.Builder
		arr.WriteString(`<array><data>`)
		for _, fp := range t.files {
			arr.WriteString(`<value><struct>`)
			member(&arr, "path", str(fp))
			arr.WriteString(`</struct></value>`)
		}
		arr.WriteString(`</data></array>`)
		member(&b, "files", arr.String())
	}
	b.WriteString(`</struct>`)
	return b.String()
}

func member(b *strings.Builder, name, valueInner string) {
	b.WriteString(`<member><name>`)
	b.WriteString(name)
	b.WriteString(`</name><value>`)
	b.WriteString(valueInner)
	b.WriteString(`</value></member>`)
}

func str(s string) string { return `<string>` + s + `</string>` }
func intStr(n int64) string {
	if n == 0 {
		return "0"
	}
	// Simple int64-to-string; avoid strconv import in test.
	s := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+(n%10))) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

func TestGetItems_Fanout3Calls(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellActive", tellResponse(torrent{
		gid: "g1", infoHash: "abc", status: "active", name: "Show.S01E01",
		total: 1000, completed: 500, downloadSpeed: 100,
		files: []string{"/data/Show.S01E01/ep.mkv"},
	}.marshal()))
	h.set("aria2.tellWaiting", tellResponse(torrent{
		gid: "g2", infoHash: "def", status: "waiting", name: "Show.S01E02",
		total: 2000, files: []string{"/data/Show.S01E02/ep.mkv"},
	}.marshal()))
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g3", infoHash: "ghi", status: "complete", name: "Show.S01E03",
		total: 1500, completed: 1500, uploaded: 750,
		files: []string{"/data/Show.S01E03/ep.mkv"},
	}.marshal()))

	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	items, err := a.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items: got %d, want 3 (one per tellX call)", len(items))
	}
	// All three tellX methods fired.
	for _, m := range []string{"aria2.tellActive", "aria2.tellWaiting", "aria2.tellStopped"} {
		if h.calls[m] != 1 {
			t.Errorf("expected %s to fire once; got %d", m, h.calls[m])
		}
	}
	// Spot-check status mapping.
	if items[0].Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("items[0] active: %v, want Downloading", items[0].Status)
	}
	if items[1].Status != downloadclients.DownloadItemStatusQueued {
		t.Errorf("items[1] waiting: %v, want Queued", items[1].Status)
	}
	if items[2].Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("items[2] complete: %v, want Completed", items[2].Status)
	}
	// DownloadID upper-cased from infoHash.
	if items[0].DownloadID != "ABC" {
		t.Errorf("items[0].DownloadID: %q, want ABC", items[0].DownloadID)
	}
	// SeedRatio for the completed one: 750/1500 = 0.5.
	if items[2].SeedRatio == nil || *items[2].SeedRatio != 0.5 {
		t.Errorf("items[2].SeedRatio: %v, want 0.5", items[2].SeedRatio)
	}
}

func TestGetItems_SkipsMetadataAndRemoved(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellActive", tellResponse(
		torrent{gid: "g1", infoHash: "1", status: "removed", name: "removed-one"}.marshal(),
		torrent{gid: "g2", infoHash: "2", status: "active", name: "meta",
			files: []string{"/data/[METADATA]meta"}}.marshal(),
		torrent{gid: "g3", infoHash: "3", status: "active", name: "good", total: 100, completed: 50,
			files: []string{"/data/good.mkv"}}.marshal(),
	))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	items, _ := a.GetItems(context.Background())
	if len(items) != 1 || items[0].Title != "good" {
		t.Errorf("items: %+v, want only the non-meta, non-removed one", items)
	}
}

func TestGetItems_CanBeRemovedOnlyWhenComplete(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(
		torrent{gid: "g1", infoHash: "1", status: "complete", name: "done", total: 100, completed: 100,
			files: []string{"/d/done.mkv"}}.marshal(),
		torrent{gid: "g2", infoHash: "2", status: "error", name: "err", total: 100, completed: 50,
			files: []string{"/d/err.mkv"}}.marshal(),
	))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	items, _ := a.GetItems(context.Background())
	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2", len(items))
	}
	if !items[0].CanBeRemoved {
		t.Errorf("complete: CanBeRemoved should be true")
	}
	if items[1].CanBeRemoved {
		t.Errorf("error: CanBeRemoved should be false")
	}
	for _, it := range items {
		if it.CanMoveFiles {
			t.Errorf("aria2 never sets CanMoveFiles=true (no file-deletion support)")
		}
	}
}

func TestGetItems_OutputPath_SingleFile(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellActive", tellResponse(torrent{
		gid: "g1", infoHash: "1", status: "active", name: "x", total: 100, completed: 50,
		files: []string{"/data/single-file.mkv"},
	}.marshal()))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	items, _ := a.GetItems(context.Background())
	if len(items) != 1 || items[0].OutputPath.String() != "/data/single-file.mkv" {
		t.Errorf("OutputPath: %+v, want /data/single-file.mkv", items)
	}
}

func TestGetItems_OutputPath_LongestCommonPath(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellActive", tellResponse(torrent{
		gid: "g1", infoHash: "1", status: "active", name: "multi", total: 100, completed: 50,
		files: []string{"/data/Show/Season1/E01.mkv", "/data/Show/Season1/E02.mkv", "/data/Show/Season1/E03.mkv"},
	}.marshal()))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	items, _ := a.GetItems(context.Background())
	if len(items) != 1 {
		t.Fatalf("items: got %d, want 1", len(items))
	}
	if items[0].OutputPath.String() != "/data/Show/Season1" {
		t.Errorf("OutputPath: %q, want /data/Show/Season1 (longest common prefix)", items[0].OutputPath)
	}
}

func TestGetItems_NilProxyErrors(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	if _, err := a.GetItems(context.Background()); err == nil {
		t.Errorf("GetItems: want error (nil proxy)")
	}
}

// RemoveItem tests.
const okString = `<?xml version="1.0"?>
<methodResponse><params><param><value><string>OK</string></value></param></params></methodResponse>`

func gidEcho(gid string) string {
	return `<?xml version="1.0"?>
<methodResponse><params><param><value><string>` + gid + `</string></value></param></params></methodResponse>`
}

func TestRemoveItem_FiresForceRemoveForActive(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellActive", tellResponse(torrent{
		gid: "g1", infoHash: "ABC", status: "active", total: 100, completed: 50,
		files: []string{"/d/x"},
	}.marshal()))
	h.set("aria2.forceRemove", gidEcho("g1"))
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if err := a.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if h.calls["aria2.forceRemove"] != 1 {
		t.Errorf("forceRemove fires once; got %d", h.calls["aria2.forceRemove"])
	}
}

func TestRemoveItem_FiresRemoveDownloadResultForComplete(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g2", infoHash: "ABC", status: "complete", total: 100, completed: 100,
		files: []string{"/d/x"},
	}.marshal()))
	h.set("aria2.removeDownloadResult", okString)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if err := a.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, false); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if h.calls["aria2.removeDownloadResult"] != 1 {
		t.Errorf("removeDownloadResult fires once; got %d", h.calls["aria2.removeDownloadResult"])
	}
}

// stubDisk for the J112 deleteData-arm tests.
type stubDisk struct {
	called bool
	path   string
	err    error
}

func (s *stubDisk) DeleteFolder(path string, _ bool) error {
	s.called = true
	s.path = path
	return s.err
}

func TestRemoveItem_DeleteData_DiskProviderCalled(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g2", infoHash: "ABC", status: "complete", total: 100, completed: 100,
		files: []string{"/d/x"},
	}.marshal()))
	h.set("aria2.removeDownloadResult", okString)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	disk := &stubDisk{}
	a.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "ABC",
		OutputPath: ospath.New("/data/downloads/Foo.S01E02"),
	}
	if err := a.RemoveItem(context.Background(), item, true); err != nil {
		t.Fatal(err)
	}
	if !disk.called {
		t.Errorf("DiskProvider.DeleteFolder not called")
	}
	if disk.path != "/data/downloads/Foo.S01E02" {
		t.Errorf("DiskProvider path=%q, want /data/downloads/Foo.S01E02", disk.path)
	}
	if h.calls["aria2.removeDownloadResult"] != 1 {
		t.Errorf("aria2 removal still expected after disk arm; got %d", h.calls["aria2.removeDownloadResult"])
	}
}

func TestRemoveItem_DeleteData_NoDiskProviderWarns(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g2", infoHash: "ABC", status: "complete", total: 100, completed: 100,
	}.marshal()))
	h.set("aria2.removeDownloadResult", okString)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if err := a.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, true); err != nil {
		t.Errorf("nil DiskProvider should degrade to warn, not error: %v", err)
	}
}

func TestRemoveItem_DeleteData_EmptyOutputPathSkipsDisk(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g2", infoHash: "ABC", status: "complete", total: 100, completed: 100,
	}.marshal()))
	h.set("aria2.removeDownloadResult", okString)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	disk := &stubDisk{}
	a.SetDiskProvider(disk)
	if err := a.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "ABC"}, true); err != nil {
		t.Fatal(err)
	}
	if disk.called {
		t.Errorf("DiskProvider.DeleteFolder fired on empty OutputPath")
	}
}

func TestRemoveItem_DeleteData_DiskErrorDoesNotAbort(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.tellStopped", tellResponse(torrent{
		gid: "g2", infoHash: "ABC", status: "complete", total: 100, completed: 100,
	}.marshal()))
	h.set("aria2.removeDownloadResult", okString)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	disk := &stubDisk{err: errors.New("permission denied")}
	a.SetDiskProvider(disk)
	item := &downloadclients.Item{
		DownloadID: "ABC",
		OutputPath: ospath.New("/data/Foo"),
	}
	if err := a.RemoveItem(context.Background(), item, true); err != nil {
		t.Errorf("disk-arm failure should not abort: %v", err)
	}
	if !disk.called {
		t.Error("disk provider expected to be called")
	}
	if h.calls["aria2.removeDownloadResult"] != 1 {
		t.Errorf("aria2 removal expected after disk failure")
	}
}

func TestRemoveItem_NotFoundReturnsNilWithWarn(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	// All three tellX calls return empty arrays — no match.
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	if err := a.RemoveItem(context.Background(), &downloadclients.Item{DownloadID: "MISSING"}, false); err != nil {
		t.Errorf("RemoveItem on missing hash: got %v, want nil (logs warn)", err)
	}
}

func TestRemoveItem_NilItemErrors(t *testing.T) {
	t.Parallel()
	a := aria2.New(nil, aria2.NewSettings(), nil, nil)
	if err := a.RemoveItem(context.Background(), nil, false); err == nil {
		t.Errorf("RemoveItem(nil): want error")
	}
}

// GetStatus tests.
const sampleGlobalsResponse = `<?xml version="1.0"?>
<methodResponse><params><param><value><struct>
<member><name>dir</name><value><string>/data/aria-downloads</string></value></member>
<member><name>max-concurrent-downloads</name><value><string>5</string></value></member>
</struct></value></param></params></methodResponse>`

func TestGetStatus_PopulatesIsLocalhostAndOutputRoot(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	h.set("aria2.getGlobalOption", sampleGlobalsResponse)
	p := aria2.NewProxy(h.srv.Client(), nil)
	s := h.settings()
	// httptest binds 127.0.0.1 → IsLocalhost should be true.
	a := aria2.New(nil, s, p, nil)
	info, err := a.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !info.IsLocalhost {
		t.Errorf("IsLocalhost: false, want true (httptest binds 127.0.0.1)")
	}
	if len(info.OutputRootFolders) != 1 || info.OutputRootFolders[0].String() != "/data/aria-downloads" {
		t.Errorf("OutputRootFolders: %+v, want [/data/aria-downloads]", info.OutputRootFolders)
	}
}

// (TestGetStatus_RemoteHostNotLocalhost intentionally omitted: the
// proxy's endpointURL uses settings.Host for the actual network
// call, so we can't easily verify the IsLocalhost branch without
// either a network mock that diverges from real endpoint behaviour
// or a hosts-table override. The IsLocalhost logic is a trivial
// strings.Contains substring check; the localhost-test above
// covers the true path. Documented in Aria2's GetStatus
// doc-comment.)

// MarkItemAsImported is permanently unsupported.
func TestMarkItemAsImported_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	h := newStagedServer(t)
	p := aria2.NewProxy(h.srv.Client(), nil)
	a := aria2.New(nil, h.settings(), p, nil)
	err := a.MarkItemAsImported(context.Background(), &downloadclients.Item{DownloadID: "x"})
	if err == nil {
		t.Errorf("MarkItemAsImported: want error")
	}
}
