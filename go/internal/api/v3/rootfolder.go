package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/rootfolders"
	"sonarr.tv/sonarr/internal/signalr"
	"sonarr.tv/sonarr/internal/validation"
)

// RootFolderResource mirrors
// `src/Sonarr.Api.V3/RootFolders/RootFolderResource.cs`.
//
// FreeSpace and TotalSpace are nullable in C# (`long?`) and elide on
// write when null per STJson.WhenWritingNull; the Go port wraps the
// in-process *int64 with `omitempty` to match.
//
// IsEmpty exists on rootfolders.Folder but NOT on the C# wire shape —
// the mapper drops it. UnmappedFolders is always emitted, defaulting
// to [].
//
// Field-source mapping (C# → Go):
//
//	id              → Folder.ID
//	path            → CleanPath(Folder.Path) (matches C# `Path.GetCleanPath()`)
//	accessible      → Folder.Accessible
//	freeSpace       → Folder.FreeSpace (*int64, omitempty)
//	totalSpace      → Folder.TotalSpace (*int64, omitempty)
//	unmappedFolders → Folder.UnmappedFolders (nil → [])
type RootFolderResource struct {
	ID              int                          `json:"id"`
	Path            string                       `json:"path"`
	Accessible      bool                         `json:"accessible"`
	FreeSpace       *int64                       `json:"freeSpace,omitempty"`
	TotalSpace      *int64                       `json:"totalSpace,omitempty"`
	UnmappedFolders []rootfolders.UnmappedFolder `json:"unmappedFolders"`
}

// RootFolderLister narrows the read surface to the disk-stat populated
// form. *rootfolders.Service.AllWithUnmappedFolders satisfies it
// directly — that's the exact method the C# controller calls.
type RootFolderLister interface {
	AllWithUnmappedFolders(ctx context.Context) ([]rootfolders.Folder, error)
}

// RootFolderHandler returns the http.Handler for
// GET /api/v3/rootfolder. Mirrors
// `RootFolderController.GetRootFolders` at
// `src/Sonarr.Api.V3/RootFolders/RootFolderController.cs`.
func RootFolderHandler(lister RootFolderLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.AllWithUnmappedFolders(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		out := make([]RootFolderResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toRootFolderResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toRootFolderResource(f rootfolders.Folder) RootFolderResource {
	unmapped := f.UnmappedFolders
	if unmapped == nil {
		unmapped = []rootfolders.UnmappedFolder{}
	}
	return RootFolderResource{
		ID:              f.ID,
		Path:            rootfolders.CleanPath(f.Path),
		Accessible:      f.Accessible,
		FreeSpace:       f.FreeSpace,
		TotalSpace:      f.TotalSpace,
		UnmappedFolders: unmapped,
	}
}

// RootFolderWriter is the write surface the CRUD handlers depend on.
// *rootfolders.Service satisfies it via Get/Add/Remove. Narrowed to
// keep test doubles tight.
type RootFolderWriter interface {
	Get(ctx context.Context, id int, timeout bool) (*rootfolders.Folder, error)
	Add(ctx context.Context, rf *rootfolders.RootFolder) (*rootfolders.Folder, error)
	Remove(ctx context.Context, id int) error
}

// RootFolderEmitter mirrors the subset of *signalr.ResourceBroadcaster
// the write handlers actually call. Lets tests inject a recording
// emitter without depending on the signalr package's full surface.
// *signalr.ResourceBroadcaster[RootFolderResource] satisfies it
// directly.
type RootFolderEmitter interface {
	Created(ctx context.Context, resource RootFolderResource) error
	Deleted(ctx context.Context, resource RootFolderResource) error
}

// RootFolderByIDHandler returns GET /api/v3/rootfolder/{id}. Mirrors
// `RootFolderController.GetResourceById` at
// `src/Sonarr.Api.V3/RootFolders/RootFolderController.cs:44-49`.
//
// The C# `Get(id, timeout)` accepts a `?timeout=` query parameter
// that defaults to true; the Go port honours it via Service.Get's
// existing timeout flag.
func RootFolderByIDHandler(svc RootFolderWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseRootFolderID(w, r)
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
		writeRootFolderJSON(w, http.StatusOK, *f)
	})
}

// RootFolderCreateHandler returns POST /api/v3/rootfolder. Mirrors
// `RootFolderController.CreateRootFolder` at `:51-58`. Returns 201
// + RootFolderResource on success.
//
// Validation chain: Path required + must be absolute at the handler
// boundary (cheap); Service.Add runs the deeper filesystem checks
// (folder exists, is writable, not a duplicate) and surfaces those
// as typed errors. The handler maps each to the appropriate HTTP
// shape matching C# FluentValidation cascade behaviour.
//
// SignalR fan-out: the C# RestControllerWithSignalR base auto-fires
// BroadcastResourceChange(Created) after a successful POST; there's
// no domain event in C# for root-folder add. The Go port calls
// emitter.Created directly here for the same reason — no event-bus
// indirection.
func RootFolderCreateHandler(svc RootFolderWriter, emitter RootFolderEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeRootFolderBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("rootfolder: malformed body: %v", err))
			return
		}
		if errs := validateRootFolderResource(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		f, err := svc.Add(r.Context(), &rootfolders.RootFolder{Path: body.Path})
		switch {
		case errors.Is(err, rootfolders.ErrInvalidPath):
			writeRootFolderFieldErr(w, "Path", "Invalid path")
			return
		case errors.Is(err, rootfolders.ErrFolderNotFound):
			writeRootFolderFieldErr(w, "Path", "Folder does not exist")
			return
		case errors.Is(err, rootfolders.ErrFolderNotWritable):
			writeRootFolderFieldErr(w, "Path", "Folder is not writable")
			return
		case errors.Is(err, rootfolders.ErrDuplicatePath):
			httperr.Conflict(w, "rootfolder path already configured")
			return
		case err != nil:
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		res := toRootFolderResource(*f)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "rootfolder", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		writeRootFolderJSON(w, http.StatusCreated, *f)
	})
}

// RootFolderDeleteHandler returns DELETE /api/v3/rootfolder/{id}.
// Mirrors `RootFolderController.DeleteFolder` at `:67-71`. Returns
// 200 empty body on success, 404 envelope on miss.
//
// Fires SignalR Deleted with a minimal resource carrying only the
// id (matches the C# pattern at
// `RestControllerWithSignalR.cs:80-82` where the deleted resource
// is projected to `{Id = id}`).
func RootFolderDeleteHandler(svc RootFolderWriter, emitter RootFolderEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseRootFolderID(w, r)
		if !ok {
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 on missing id.
		// SignalR Deleted broadcast still fires on actual delete (skipped on missing-id swallow).
		err := svc.Remove(r.Context(), id)
		if err != nil && !errors.Is(err, rootfolders.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("rootfolders: %w", err), nil)
			return
		}
		if err == nil {
			signalr.FireAndLog(httperr.LoggerFromRequest(r), "rootfolder", "deleted", func() error {
				return emitter.Deleted(r.Context(), RootFolderResource{ID: id})
			})
		}
		w.WriteHeader(http.StatusOK)
	})
}

// parseRootFolderID extracts and validates the {id} path wildcard.
// Same shape as parseTagID (5.B1) — once enough resources share the
// pattern, factor into a shared helper. For now, per-resource so
// the error messages name the resource.
func parseRootFolderID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "rootfolder: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("rootfolder: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

// decodeRootFolderBody parses the request JSON. DisallowUnknownFields
// rejects payloads with stray keys — surfaces typos in client code.
func decodeRootFolderBody(r *http.Request) (*RootFolderResource, error) {
	var t RootFolderResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateRootFolderResource runs the handler-boundary checks the C#
// `SharedValidator.RuleFor(c => c.Path)` chain applies before the
// service-side validators take over. .IsValidPath() in C# enforces
// "non-empty, absolute, no invalid filesystem chars"; the Go port
// implements the first two as cheap request-time checks and defers
// the deep filesystem-state checks to *rootfolders.Service.Add.
func validateRootFolderResource(res *RootFolderResource) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Path", res.Path) {
		return errs
	}
	if !filepath.IsAbs(res.Path) {
		errs.Add("Path", "Path must be absolute")
	}
	return errs
}

// writeRootFolderFieldErr emits a single-failure FluentValidation
// 400 for a service-side rejection that maps to a per-field error.
// Used for ErrInvalidPath / ErrFolderNotFound / ErrFolderNotWritable
// where the wire shape should match the field-level validation
// failures the C# pipeline produces via `SetValidator(...)` calls.
func writeRootFolderFieldErr(w http.ResponseWriter, property, msg string) {
	httperr.WriteValidation(w, []httperr.Failure{{
		PropertyName: property,
		ErrorMessage: msg,
		Severity:     "error",
	}})
}

// writeRootFolderJSON emits a single Folder projected to RootFolderResource.
func writeRootFolderJSON(w http.ResponseWriter, status int, f rootfolders.Folder) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toRootFolderResource(f))
	_, _ = w.Write(buf)
}
