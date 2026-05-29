package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
)

// LogResource mirrors `src/Sonarr.Api.V3/Logs/LogResource.cs`. The
// per-row shape inside the PagingResource envelope. Shipped as a
// skeleton today — the Go log-DB hasn't been ported so every list
// read returns an empty page (matches the C# `LogDbEnabled=false`
// branch that short-circuits the controller at LogController.cs:28).
type LogResource struct {
	ID            int    `json:"id"`
	Time          string `json:"time"`
	Exception     string `json:"exception,omitempty"`
	ExceptionType string `json:"exceptionType,omitempty"`
	Level         string `json:"level"`
	Logger        string `json:"logger"`
	Message       string `json:"message"`
	Method        string `json:"method,omitempty"`
}

// LogLister is the read side GET /api/v3/log depends on. The production
// adapter (cmd/sonarr) wraps the log-DB repo and maps rows → LogResource;
// a nil lister (log DB disabled) yields an empty page, matching the C#
// `LogDbEnabled == false` short-circuit. sortKey is the physical sort
// ("id"/"time"); levels is the cumulative level filter set (empty = all).
type LogLister interface {
	PagedLogs(ctx context.Context, page, pageSize int, sortKey string, desc bool, levels []string) (records []LogResource, total int, err error)
}

// LogHandler returns GET /api/v3/log. Mirrors `LogController.GetLogs`
// at `LogController.cs:26-78`: it reads the paging params + the `level`
// query param (expanded to a cumulative level set via
// [expandLogLevelFilter]), queries the lister, and rewrites the SortKey
// back to "time" on the response (C# stores "id" internally for the SQL
// but presents "time" to the UI). A nil lister returns an empty page
// (the C# `LogDbEnabled == false` behaviour).
func LogHandler(lister LogLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "time")
		// C# allows id/time as input sort keys and uses "id" internally
		// (Id is monotonic with Time and indexed); the response reports
		// the user-facing "time".
		internalSort := "id"
		if req.SortKey == "time" || req.SortKey == "id" {
			internalSort = "id"
		}
		desc := !strings.HasPrefix(strings.ToLower(req.SortDirection), "asc")
		levels := expandLogLevelFilter(r.URL.Query().Get("level"))

		records := []LogResource{}
		total := 0
		if lister != nil {
			rs, t, err := lister.PagedLogs(r.Context(), req.Page, req.PageSize, internalSort, desc, levels)
			if err != nil {
				httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("log: %w", err), nil)
				return
			}
			if rs != nil {
				records = rs
			}
			total = t
		}
		resp := PagingResource[LogResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: records, SortDirection: req.SortDirection,
			SortKey: "time", TotalRecords: total,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}

// expandLogLevelFilter ports the cumulative `level` filter cascade at
// `LogController.cs:46-69`: each level enables everything more severe.
// An empty/unknown value returns nil (no filter).
func expandLogLevelFilter(level string) []string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "fatal":
		return []string{"Fatal"}
	case "error":
		return []string{"Fatal", "Error"}
	case "warn":
		return []string{"Fatal", "Error", "Warn"}
	case "info":
		return []string{"Fatal", "Error", "Warn", "Info"}
	case "debug":
		return []string{"Fatal", "Error", "Warn", "Info", "Debug"}
	case "trace":
		return []string{"Fatal", "Error", "Warn", "Info", "Debug", "Trace"}
	default:
		return nil
	}
}
