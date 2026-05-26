// Package systemtask ports the v5 TaskController family at
// /api/v5/system/task.
//
// Source: src/Sonarr.Api.V5/System/Tasks/TaskController.cs (65 LOC) +
// src/Sonarr.Api.V5/System/Tasks/TaskResource.cs (14 LOC).
//
// 2 endpoints (GET list + GET by-id), both 200. ONE wire-shape
// divergence from v3 Go-port: v5 emits an additional `lastDuration`
// field (TimeSpan, computed as LastExecution - LastStartTime), per
// `TaskController.cs:56`. v3 Go-port TaskResource omits the field
// entirely (`internal/api/v3/system_task.go:29-37`) — even though
// v3 C# carries the computed property at TaskResource.cs:15, the
// Go-port deliberately left it out (J231 redacted the timing-derived
// field from the v3 corpus rather than emit it). v5 surface must
// include it to match the v5 openapi schema.
//
// `lastStartTime` shape: v5 openapi declares it as non-nullable
// (`type: string, format: date-time`); v3 Go-port uses `*time.Time`
// with `omitempty`. v5 emits the field unconditionally — when the
// underlying Task.LastStartTime is nil, surface as `time.Time{}`
// (zero value), which JSON-marshals to `"0001-01-01T00:00:00Z"`
// matching .NET's default(DateTime) serialisation.
//
// `lastDuration` format: .NET TimeSpan serialises to a string with
// the canonical pattern `[-][d.]hh:mm:ss[.fffffff]` — zero is
// `"00:00:00"`, one minute is `"00:01:00"`, one day + one minute is
// `"1.00:01:00"`, negative values are prefixed with `-`. Implemented
// in formatTimeSpan().
//
// SignalR: v5 declares IHandle<CommandExecutedEvent> emitting
// ModelAction.Sync on the task channel. Wave 2 mop-up — not yet
// wired on either v3 or v5 Go-port side.
package systemtask

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// Lister is re-exported from v3 — same backing service, same shape.
type Lister = apiv3.SystemTaskLister

// Resource mirrors src/Sonarr.Api.V5/System/Tasks/TaskResource.cs.
//
// Eight wire fields. Diff vs v3 Go-port apiv3.TaskResource:
//   - adds lastDuration (TimeSpan as string)
//   - lastStartTime is non-nullable (always emitted)
type Resource struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	TaskName      string    `json:"taskName"`
	Interval      int       `json:"interval"`
	LastExecution time.Time `json:"lastExecution"`
	LastStartTime time.Time `json:"lastStartTime"`
	NextExecution time.Time `json:"nextExecution"`
	LastDuration  string    `json:"lastDuration"`
}

// ListHandler returns GET /api/v5/system/task. Mirrors C#
// `TaskController.GetAll` — ordered by .Name ascending.
func ListHandler(svc Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tasks := svc.GetAll(r.Context())
		out := make([]Resource, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, taskToResource(t))
		}
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].Name < out[j].Name
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ByIDHandler returns GET /api/v5/system/task/{id}. Mirrors
// `TaskController.GetResourceById` — 404 on missing id.
func ByIDHandler(svc Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "system/task: invalid id")
			return
		}
		for _, t := range svc.GetAll(r.Context()) {
			if t.ID == id {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				buf, _ := json.Marshal(taskToResource(t))
				_, _ = w.Write(buf)
				return
			}
		}
		httperr.NotFound(w, fmt.Sprintf("system/task %d not found", id))
	})
}

func taskToResource(t scheduledtasks.Task) Resource {
	name := splitCamelCase(t.TypeName)
	var lastStart time.Time
	if t.LastStartTime != nil {
		lastStart = *t.LastStartTime
	}
	return Resource{
		ID:            t.ID,
		Name:          name,
		TaskName:      t.TypeName,
		Interval:      t.Interval,
		LastExecution: t.LastExecution,
		LastStartTime: lastStart,
		NextExecution: t.LastExecution.Add(time.Duration(t.Interval) * time.Minute),
		LastDuration:  formatTimeSpan(t.LastExecution.Sub(lastStart)),
	}
}

// splitCamelCase is a local copy of the v3 helper — kept private to
// avoid widening the v3 export surface for a single re-use. Mirrors
// command.go:307-323: inserts a space before each uppercase letter
// that follows a lowercase letter or a digit.
func splitCamelCase(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev := rune(s[i-1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatTimeSpan renders a Go time.Duration in .NET's default
// TimeSpan ToString("c") layout: `[-][d.]hh:mm:ss[.fffffff]`. Used
// for the v5 lastDuration wire field.
func formatTimeSpan(d time.Duration) string {
	neg := ""
	if d < 0 {
		neg = "-"
		d = -d
	}
	day := time.Hour * 24
	days := int64(d / day)
	d -= time.Duration(days) * day
	h := int64(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int64(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int64(d / time.Second)
	d -= time.Duration(s) * time.Second
	// .NET ticks are 100ns units; 7 digits of fractional seconds.
	ticks := int64(d / (100 * time.Nanosecond))
	switch {
	case days > 0 && ticks > 0:
		return fmt.Sprintf("%s%d.%02d:%02d:%02d.%07d", neg, days, h, m, s, ticks)
	case days > 0:
		return fmt.Sprintf("%s%d.%02d:%02d:%02d", neg, days, h, m, s)
	case ticks > 0:
		return fmt.Sprintf("%s%02d:%02d:%02d.%07d", neg, h, m, s, ticks)
	default:
		return fmt.Sprintf("%s%02d:%02d:%02d", neg, h, m, s)
	}
}

// satisfy compile-time check that v3 lister continues to fit.
var _ Lister = (apiv3.SystemTaskLister)(nil)
