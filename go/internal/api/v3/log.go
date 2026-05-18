package v3

import (
	"encoding/json"
	"net/http"
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

// LogHandler returns GET /api/v3/log. Mirrors
// `LogController.GetLogs` at `LogController.cs:26-78`. Today this
// always returns an empty paged response — the log DB plumbing isn't
// ported. The C# action has the same behaviour when
// ConfigFileProvider.LogDbEnabled is false (the common production
// default for log-DB-disabled deployments). The SortKey wire shape
// is preserved ("time" override matches the C# action's behaviour:
// it stores "id" internally for the SQL query and rewrites the
// response to "time" so the React UI sees the user-facing key).
func LogHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parsePagingRequest(r, "time")
		if req.SortKey == "id" {
			req.SortKey = "time"
		}
		resp := PagingResource[LogResource]{
			Page: req.Page, PageSize: req.PageSize,
			Records: []LogResource{}, SortDirection: req.SortDirection,
			SortKey: req.SortKey, TotalRecords: 0,
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(resp)
		_, _ = w.Write(buf)
	})
}
