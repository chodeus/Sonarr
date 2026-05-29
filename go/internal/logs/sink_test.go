package logs_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/logs"
)

func newSink(t *testing.T) (*logs.Sink, *logs.Repo) {
	t.Helper()
	r, err := logs.NewRepo(datastoretest.OpenSQLiteLog(t))
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	return logs.NewSink(r, slog.LevelInfo, 64), r
}

func rec(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC), level, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// Enabled gates at the configured min level (Info+).
func TestSink_Enabled(t *testing.T) {
	t.Parallel()
	s, _ := newSink(t)
	defer s.Close()
	if s.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Debug should be disabled")
	}
	if !s.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should be enabled")
	}
	if !s.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled")
	}
}

// A handled record is persisted with mapped fields after Close flushes.
func TestSink_HandlePersists(t *testing.T) {
	t.Parallel()
	s, r := newSink(t)
	if err := s.Handle(context.Background(), rec(slog.LevelWarn, "disk full")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_ = s.Close() // flush

	page, err := r.Paged(context.Background(), logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true})
	if err != nil {
		t.Fatalf("Paged: %v", err)
	}
	if page.TotalRecords != 1 {
		t.Fatalf("rows = %d, want 1", page.TotalRecords)
	}
	got := page.Records[0]
	if got.Message != "disk full" || got.Level != "Warn" {
		t.Errorf("row = %+v, want msg='disk full' level=Warn", got)
	}
}

// An "err" attribute maps to Exception/ExceptionType and is folded into
// the message (mirrors C# DatabaseTarget.Write).
func TestSink_ExceptionMapping(t *testing.T) {
	t.Parallel()
	s, r := newSink(t)
	err := errors.New("boom")
	if e := s.Handle(context.Background(), rec(slog.LevelError, "import failed", slog.Any("err", err))); e != nil {
		t.Fatalf("Handle: %v", e)
	}
	_ = s.Close()

	page, _ := r.Paged(context.Background(), logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true})
	if page.TotalRecords != 1 {
		t.Fatalf("rows = %d, want 1", page.TotalRecords)
	}
	got := page.Records[0]
	if got.Exception != "boom" {
		t.Errorf("Exception = %q, want boom", got.Exception)
	}
	if got.ExceptionType != "*errors.errorString" {
		t.Errorf("ExceptionType = %q, want *errors.errorString", got.ExceptionType)
	}
	if got.Message != "import failed: boom" {
		t.Errorf("Message = %q, want 'import failed: boom'", got.Message)
	}
}

// WithAttrs carrying "component" tags the persisted Logger.
func TestSink_WithAttrsLogger(t *testing.T) {
	t.Parallel()
	s, r := newSink(t)
	h := s.WithAttrs([]slog.Attr{slog.String("component", "DiskScan")})
	if err := h.Handle(context.Background(), rec(slog.LevelInfo, "scanning")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_ = s.Close() // shares the same core/writer

	page, _ := r.Paged(context.Background(), logs.PageQuery{Page: 1, PageSize: 50, SortKey: "time", Desc: true})
	if page.TotalRecords != 1 || page.Records[0].Logger != "DiskScan" {
		t.Errorf("Logger = %q (rows=%d), want DiskScan", func() string {
			if page.TotalRecords > 0 {
				return page.Records[0].Logger
			}
			return ""
		}(), page.TotalRecords)
	}
}

// Close flushes every buffered record before returning.
func TestSink_CloseFlushesAll(t *testing.T) {
	t.Parallel()
	s, r := newSink(t)
	for i := 0; i < 20; i++ {
		if err := s.Handle(context.Background(), rec(slog.LevelInfo, "m")); err != nil {
			t.Fatalf("Handle %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	page, _ := r.Paged(context.Background(), logs.PageQuery{Page: 1, PageSize: 100, SortKey: "time", Desc: true})
	if page.TotalRecords != 20 {
		t.Errorf("after flush rows = %d, want 20", page.TotalRecords)
	}
}

// Close is idempotent.
func TestSink_CloseIdempotent(t *testing.T) {
	t.Parallel()
	s, _ := newSink(t)
	_ = s.Close()
	_ = s.Close()
}
