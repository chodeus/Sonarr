// Package rootfolder is the v5 wire surface for /api/v5/rootfolder.
//
// The v5 controller (src/Sonarr.Api.V5/RootFolders/RootFolderController.cs)
// is at 128 LOC of C# source. Combo slice — TWO divergences vs v3:
//
//  1. Body SHAPE: v5 RootFolderResource adds an `isEmpty` field that
//     v3 doesn't carry (per the C# v3 source `RootFolderResource.cs`).
//     The underlying `rootfolders.Folder` model already computes
//     IsEmpty (per `internal/rootfolders/logic.go:41` + service:374),
//     it just isn't projected on the v3 wire. v5 projects it.
//
//  2. Status code on DELETE: v3 emits 200 + empty body; v5 emits 204
//     NoContent + empty body via `TypedResults.NoContent`.
//
// Patterns combined (per J254 + J257):
//   - List / ByID / Create need custom v5 handlers — they project the
//     v5 Resource shape (with IsEmpty) so path-aliasing the v3 handler
//     would emit the v3-without-IsEmpty shape.
//   - Delete wraps the v3 handler via `v5util.RewriteSuccessTo204` —
//     the wrapper only changes the HTTP status; the underlying SignalR
//     Deleted broadcast wiring remains intact (and continues to emit
//     the v3 shape, since v5 SignalR fan-out is a separate per-version
//     concern not yet wired).
//
// SignalR fan-out for v5 CREATE: the v5-specific CreateHandler does
// NOT fire any SignalR broadcast. v5 SignalR routes haven't been
// added yet (would require v5-specific SignalR channels in
// signalr_wiring.go). When that lands, an Emitter dep can be plumbed
// through; for now Create just returns 201 + the v5 Resource.
//
// Wave 3 v5 endpoint family port — 128 LOC C# source. J262.
package rootfolder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/api/v5/internal/v5util"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/rootfolders"
)

// Resource is the v5 RootFolderResource wire shape. Adds `isEmpty`
// vs v3 (id / path / accessible / **isEmpty** / freeSpace / totalSpace
// / unmappedFolders).
type Resource struct {
	ID              int                          `json:"id"`
	Path            string                       `json:"path"`
	Accessible      bool                         `json:"accessible"`
	IsEmpty         bool                         `json:"isEmpty"`
	FreeSpace       *int64                       `json:"freeSpace,omitempty"`
	TotalSpace      *int64                       `json:"totalSpace,omitempty"`
	UnmappedFolders []rootfolders.UnmappedFolder `json:"unmappedFolders"`
}

// Lister is an alias of the v3 read interface.
type Lister = apiv3.RootFolderLister

// Writer is an alias of the v3 write interface.
type Writer = apiv3.RootFolderWriter

// toResource projects a rootfolders.Folder onto the v5 wire shape.
// Same projection as v3's `toRootFolderResource` plus IsEmpty + the
// path-cleaning helper. UnmappedFolders nil → `[]` to match v3
// behaviour.
//
// J262.
func toResource(f rootfolders.Folder) Resource {
	unmapped := f.UnmappedFolders
	if unmapped == nil {
		unmapped = []rootfolders.UnmappedFolder{}
	}
	return Resource{
		ID:              f.ID,
		Path:            rootfolders.CleanPath(f.Path),
		Accessible:      f.Accessible,
		IsEmpty:         f.IsEmpty,
		FreeSpace:       f.FreeSpace,
		TotalSpace:      f.TotalSpace,
		UnmappedFolders: unmapped,
	}
}

// ListHandler returns GET /api/v5/rootfolder.
//
// Mirrors `RootFolderController.GetRootFolders` at
// `src/Sonarr.Api.V5/RootFolders/RootFolderController.cs:64-68`:
// `TypedResults.Ok(_rootFolderService.AllWithUnmappedFolders().ToResource())`.
//
// J262.
func ListHandler(lister Lister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.AllWithUnmappedFolders(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		out := make([]Resource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toResource(row))
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// ByIDHandler returns GET /api/v5/rootfolder/{id}.
//
// Mirrors `RootFolderController.GetResourceById` (inherited) +
// the `?timeout=` query param at `:48-52`.
//
// J262.
func ByIDHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		timeout := true
		if v := r.URL.Query().Get("timeout"); v != "" {
			if parsed, err := strconv.ParseBool(v); err == nil {
				timeout = parsed
			}
		}
		f, err := svc.Get(r.Context(), id, timeout)
		if errors.Is(err, rootfolders.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("rootfolder %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		writeJSON(w, http.StatusOK, toResource(*f))
	})
}

// CreateHandler returns POST /api/v5/rootfolder.
//
// Mirrors `RootFolderController.CreateRootFolder` at `:55-61`:
// `TypedCreated(...)` → 201 + v5 Resource body. Error paths match
// the v3 handler's validation cascade (invalid path / not found /
// not writable / duplicate).
//
// No SignalR emit — v5 SignalR fan-out is a future slice when v5
// channels are added to signalr_wiring.go.
//
// J262.
func CreateHandler(svc Writer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("rootfolder: malformed body: %v", err))
			return
		}
		if body.Path == "" {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "Path",
				ErrorMessage: "'Path' must not be empty.",
				Severity:     "error",
			}})
			return
		}
		f, err := svc.Add(r.Context(), &rootfolders.RootFolder{Path: body.Path})
		switch {
		case errors.Is(err, rootfolders.ErrInvalidPath):
			fieldErr(w, "Path", "Invalid path")
			return
		case errors.Is(err, rootfolders.ErrFolderNotFound):
			fieldErr(w, "Path", "Folder does not exist")
			return
		case errors.Is(err, rootfolders.ErrFolderNotWritable):
			fieldErr(w, "Path", "Folder is not writable")
			return
		case errors.Is(err, rootfolders.ErrDuplicatePath):
			httperr.Conflict(w, "rootfolder path already configured")
			return
		case err != nil:
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		writeJSON(w, http.StatusCreated, toResource(*f))
	})
}

// DeleteHandler returns DELETE /api/v5/rootfolder/{id}.
//
// Mirrors `RootFolderController.DeleteFolder` at `:71-75`:
// `TypedResults.NoContent()` → 204 with empty body. The v3 handler
// emits 200 + empty body (J232 idempotency posture); v5 wrapper
// demotes to 204.
//
// The wrapped v3 handler takes an emitter — passed nil here since
// v5 SignalR fan-out is not yet wired. nil-tolerance: the v3 handler
// guards with `if emitter != nil` before broadcasting (verify via
// inspection — this comment is a contract; if v3 changes, this
// becomes a 500-on-DELETE crash and tests will catch).
//
// J262.
func DeleteHandler(svc Writer) http.Handler {
	return v5util.RewriteSuccessTo204(apiv3.RootFolderDeleteHandler(svc, nilEmitter{}))
}

// nilEmitter is a no-op RootFolderEmitter used when v5 SignalR
// fan-out is not yet wired. Production wiring binds a real emitter
// once v5 channels are added.
type nilEmitter struct{}

func (nilEmitter) Created(_ context.Context, _ apiv3.RootFolderResource) error { return nil }
func (nilEmitter) Deleted(_ context.Context, _ apiv3.RootFolderResource) error { return nil }

// ----- private helpers -----

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "rootfolder: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, "rootfolder: invalid id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(v)
	_, _ = w.Write(buf)
}

func fieldErr(w http.ResponseWriter, property, msg string) {
	httperr.WriteValidation(w, []httperr.Failure{{
		PropertyName: property,
		ErrorMessage: msg,
		Severity:     "error",
	}})
}
