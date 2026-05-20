package rtorrent_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/rtorrent"
)

// multicallResponse builds a synthetic d.multicall.2 response with
// the supplied per-torrent rows. Each row is a 12-tuple matching
// the C# column projection at `RTorrentProxy.cs:42-65`.
type fakeRow struct {
	name, hash, path, category           string
	totalSize, leftSize, downRate, ratio int64
	isOpen, isActive, complete           bool
	finishedTime                         int64
}

func multicallResponse(rows ...fakeRow) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?>` + "\n")
	sb.WriteString(`<methodResponse><params><param><value><array><data>`)
	for _, r := range rows {
		sb.WriteString(`<value><array><data>`)
		sb.WriteString(strVal(r.name))
		sb.WriteString(strVal(r.hash))
		sb.WriteString(strVal(r.path))
		sb.WriteString(strVal(r.category))
		sb.WriteString(i8Val(r.totalSize))
		sb.WriteString(i8Val(r.leftSize))
		sb.WriteString(i8Val(r.downRate))
		sb.WriteString(i8Val(r.ratio))
		sb.WriteString(i8Val(boolInt(r.isOpen)))
		sb.WriteString(i8Val(boolInt(r.isActive)))
		sb.WriteString(i8Val(boolInt(r.complete)))
		sb.WriteString(i8Val(r.finishedTime))
		sb.WriteString(`</data></array></value>`)
	}
	sb.WriteString(`</data></array></value></param></params></methodResponse>`)
	return sb.String()
}

func strVal(s string) string {
	return fmt.Sprintf(`<value><string>%s</string></value>`, s)
}

func i8Val(n int64) string {
	return fmt.Sprintf(`<value><i8>%d</i8></value>`, n)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func TestGetItems_HappyPath(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{
			name: "Show.S01E01", hash: "abc123", path: "/data/downloads/Show.S01E01",
			category: "tv-sonarr", totalSize: 1000, leftSize: 0, downRate: 0, ratio: 1500,
			isOpen: true, isActive: false, complete: true, finishedTime: 1715000000,
		},
		fakeRow{
			name: "Show.S01E02", hash: "def456", path: "/data/downloads/Show.S01E02",
			category: "tv-sonarr", totalSize: 2000, leftSize: 500, downRate: 100, ratio: 0,
			isOpen: true, isActive: true, complete: false,
		},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)

	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items: got %d, want 2", len(items))
	}

	// First item — completed.
	first := items[0]
	if first.DownloadID != "ABC123" {
		t.Errorf("DownloadID: got %q, want ABC123 (uppercased)", first.DownloadID)
	}
	if first.Status != downloadclients.DownloadItemStatusCompleted {
		t.Errorf("Status: got %v, want Completed", first.Status)
	}
	if first.SeedRatio == nil || *first.SeedRatio != 1.5 {
		t.Errorf("SeedRatio: got %v, want 1.5 (1500/1000)", first.SeedRatio)
	}

	// Second item — downloading.
	second := items[1]
	if second.Status != downloadclients.DownloadItemStatusDownloading {
		t.Errorf("Status: got %v, want Downloading", second.Status)
	}
	if second.RemainingTime == nil {
		t.Errorf("RemainingTime: nil; want non-nil (downloading with DownRate > 0)")
	} else if second.RemainingTime.Seconds() != 5 {
		t.Errorf("RemainingTime: got %v, want 5s (500/100)", second.RemainingTime)
	}
}

func TestGetItems_CategoryFilter(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{name: "tv", hash: "1", path: "/d", category: "tv-sonarr",
			totalSize: 1, leftSize: 0, isOpen: true, complete: true},
		fakeRow{name: "movies", hash: "2", path: "/d", category: "movies-radarr",
			totalSize: 1, leftSize: 0, isOpen: true, complete: true},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)

	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 1 || items[0].Title != "tv" {
		t.Errorf("category filter: got %d items %+v, want only the tv-sonarr one", len(items), items)
	}
}

func TestGetItems_NoCategoryFilterWhenEmpty(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{name: "a", hash: "1", path: "/d", category: "tv-sonarr",
			totalSize: 1, leftSize: 0, isOpen: true},
		fakeRow{name: "b", hash: "2", path: "/d", category: "other",
			totalSize: 1, leftSize: 0, isOpen: true},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "" // empty → no filter
	rt := rtorrent.New(nil, s, p, nil)

	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("no-filter: got %d items, want 2 (no category filter)", len(items))
	}
}

func TestGetItems_SkipsEmptyOrRelativePath(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{name: "empty-path", hash: "1", path: "", category: "tv-sonarr"},
		fakeRow{name: "relative-path", hash: "2", path: "./relative", category: "tv-sonarr"},
		fakeRow{name: "ok", hash: "3", path: "/abs/path", category: "tv-sonarr",
			totalSize: 1, isOpen: true},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)

	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 1 || items[0].Title != "ok" {
		t.Errorf("path filter: got %+v, want only the abs-path one", items)
	}
}

func TestGetItems_StatusMapping(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{name: "completed", hash: "1", path: "/d", category: "tv-sonarr",
			complete: true, isOpen: true},
		fakeRow{name: "downloading", hash: "2", path: "/d", category: "tv-sonarr",
			isActive: true, isOpen: true},
		fakeRow{name: "paused", hash: "3", path: "/d", category: "tv-sonarr",
			isOpen: false, isActive: false},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)

	items, _ := rt.GetItems(context.Background())
	if len(items) != 3 {
		t.Fatalf("items: got %d, want 3", len(items))
	}
	want := []downloadclients.DownloadItemStatus{
		downloadclients.DownloadItemStatusCompleted,
		downloadclients.DownloadItemStatusDownloading,
		downloadclients.DownloadItemStatusPaused,
	}
	for i, w := range want {
		if items[i].Status != w {
			t.Errorf("item[%d] Status: got %v, want %v", i, items[i].Status, w)
		}
	}
}

func TestGetItems_NilProxyErrors(t *testing.T) {
	t.Parallel()
	rt := rtorrent.New(nil, rtorrent.NewSettings(), nil, nil)
	if _, err := rt.GetItems(context.Background()); err == nil {
		t.Errorf("GetItems: want error when nil proxy")
	}
}

func TestGetItems_EmptyResultSet(t *testing.T) {
	t.Parallel()
	body := multicallResponse()
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	rt := rtorrent.New(nil, h.settings("RPC2"), p, nil)
	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("empty result: got %d items, want 0", len(items))
	}
}

func TestGetItems_URLDecodesCategory(t *testing.T) {
	t.Parallel()
	body := multicallResponse(
		fakeRow{name: "x", hash: "1", path: "/d", category: "tv%2Dsonarr",
			totalSize: 1, isOpen: true},
	)
	h := newXMLRPCServer(t, body)
	p := rtorrent.NewProxy(h.srv.Client(), nil)
	s := h.settings("RPC2")
	s.TvCategory = "tv-sonarr"
	rt := rtorrent.New(nil, s, p, nil)
	items, err := rt.GetItems(context.Background())
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("url-decoded category should pass filter: got %d items", len(items))
	}
	if items[0].Category != "tv-sonarr" {
		t.Errorf("Category: got %q, want url-decoded 'tv-sonarr'", items[0].Category)
	}
}
