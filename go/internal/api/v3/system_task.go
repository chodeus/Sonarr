package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/scheduledtasks"
)

// TaskResource mirrors `src/Sonarr.Api.V3/System/Tasks/TaskResource.cs`.
// 7 fields covering the scheduled-task metadata + computed
// NextExecution.
//
// Wire shape:
//
//	id              int
//	name            string  // splitCamelCase(TaskName) — "RefreshSeries" -> "Refresh Series"
//	taskName        string  // canonical TypeName without "Command" suffix
//	interval        int     // minutes between runs
//	lastExecution   time.Time
//	lastStartTime   *time.Time  // nullable
//	nextExecution   time.Time   // LastExecution + Interval minutes
type TaskResource struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	TaskName      string     `json:"taskName"`
	Interval      int        `json:"interval"`
	LastExecution time.Time  `json:"lastExecution"`
	LastStartTime *time.Time `json:"lastStartTime,omitempty"`
	NextExecution time.Time  `json:"nextExecution"`
}

// SystemTaskLister is the read surface for /api/v3/system/task. The
// production *scheduledtasks.Service.GetAll satisfies this directly.
type SystemTaskLister interface {
	GetAll(ctx context.Context) []scheduledtasks.Task
}

// SystemTaskHandler returns GET /api/v3/system/task. Mirrors C#
// `TaskController.GetAll` at `:25-32`:
//
//	return _taskManager.GetAll()
//	    .Select(ConvertToResource)
//	    .OrderBy(t => t.Name)
//	    .ToList();
//
// Ordering matches C# default `OrderBy(t => t.Name)` (current-culture
// ascending — the Go port uses ordinal comparison which matches the
// recorded golden on ASCII task names; locale-sensitive comparison
// can be revisited if a non-ASCII task name lands).
func SystemTaskHandler(svc SystemTaskLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tasks := svc.GetAll(r.Context())
		out := make([]TaskResource, 0, len(tasks))
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

// SystemTaskByIDHandler returns GET /api/v3/system/task/{id}. Mirrors
// `TaskController.GetResourceById` at `:34-44`. C# returns 404 when
// the id misses (the SingleOrDefault + null check throws
// NotFoundException via the base class).
func SystemTaskByIDHandler(svc SystemTaskLister) http.Handler {
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

func taskToResource(t scheduledtasks.Task) TaskResource {
	return TaskResource{
		ID:            t.ID,
		Name:          splitCamelCase(t.TypeName),
		TaskName:      t.TypeName,
		Interval:      t.Interval,
		LastExecution: t.LastExecution,
		LastStartTime: t.LastStartTime,
		NextExecution: t.LastExecution.Add(time.Duration(t.Interval) * time.Minute),
	}
}
