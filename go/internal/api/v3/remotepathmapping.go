package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/remotepathmappings"
	"sonarr.tv/sonarr/internal/validation"
)

// RemotePathMappingResource mirrors
// `src/Sonarr.Api.V3/RemotePathMappings/RemotePathMappingResource.cs`.
// Three plain-string fields plus the inherited Id — every mapping
// row is a direct passthrough.
//
// Field-source mapping (C# → Go):
//
//	id          → Mapping.ID
//	host        → Mapping.Host
//	remotePath  → Mapping.RemotePath
//	localPath   → Mapping.LocalPath
type RemotePathMappingResource struct {
	ID         int    `json:"id"`
	Host       string `json:"host"`
	RemotePath string `json:"remotePath"`
	LocalPath  string `json:"localPath"`
}

// RemotePathMappingLister narrows the read surface to All().
// *remotepathmappings.Service satisfies it.
type RemotePathMappingLister interface {
	All(ctx context.Context) ([]remotepathmappings.Mapping, error)
}

// RemotePathMappingHandler returns the http.Handler for
// GET /api/v3/remotepathmapping. Mirrors
// `RemotePathMappingController.GetMappings` at
// `src/Sonarr.Api.V3/RemotePathMappings/RemotePathMappingController.cs`.
func RemotePathMappingHandler(lister RemotePathMappingLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("remotepathmappings: %w", err), nil)
			return
		}
		out := make([]RemotePathMappingResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, RemotePathMappingResource{
				ID:         row.ID,
				Host:       row.Host,
				RemotePath: row.RemotePath,
				LocalPath:  row.LocalPath,
			})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ---------- 5.B9: CRUD ----------

// RemotePathMappingWriter is the write surface. *remotepathmappings.
// Service satisfies it via the existing Get/Add/Update/Remove.
// RemotePathMappingController in C# extends RestController<T> — no SignalR.
type RemotePathMappingWriter interface {
	Get(ctx context.Context, id int) (*remotepathmappings.Mapping, error)
	Add(ctx context.Context, m *remotepathmappings.Mapping) (*remotepathmappings.Mapping, error)
	Update(ctx context.Context, m *remotepathmappings.Mapping) (*remotepathmappings.Mapping, error)
	Remove(ctx context.Context, id int) error
}

// RemotePathMappingByIDHandler returns GET /api/v3/remotepathmapping/{id}.
// Mirrors `RemotePathMappingController.GetResourceById` at
// `src/Sonarr.Api.V3/RemotePathMappings/RemotePathMappingController.cs:49-52`.
func RemotePathMappingByIDHandler(svc RemotePathMappingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseRemotePathMappingID(w, r)
		if !ok {
			return
		}
		m, err := svc.Get(r.Context(), id)
		if errors.Is(err, remotepathmappings.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("remotepathmapping %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("remotepathmappings: %w", err), nil)
			return
		}
		writeRemotePathMappingJSON(w, http.StatusOK, *m)
	})
}

// RemotePathMappingCreateHandler returns POST /api/v3/remotepathmapping.
// Mirrors `CreateMapping` at `:54-61`. 201 + body on success.
func RemotePathMappingCreateHandler(svc RemotePathMappingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeRemotePathMappingBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("remotepathmapping: malformed body: %v", err))
			return
		}
		if errs := validateRemotePathMapping(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := remotepathmappings.Mapping{
			Host:       body.Host,
			RemotePath: body.RemotePath,
			LocalPath:  body.LocalPath,
		}
		inserted, err := svc.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("remotepathmappings: %w", err), nil)
			return
		}
		writeRemotePathMappingJSON(w, http.StatusCreated, *inserted)
	})
}

// RemotePathMappingUpdateHandler returns PUT /api/v3/remotepathmapping/{id}.
// Mirrors `UpdateMapping` at `:76-82`. Path id wins over body id.
func RemotePathMappingUpdateHandler(svc RemotePathMappingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseRemotePathMappingID(w, r)
		if !ok {
			return
		}
		body, err := decodeRemotePathMappingBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("remotepathmapping: malformed body: %v", err))
			return
		}
		body.ID = id
		if errs := validateRemotePathMapping(body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := remotepathmappings.Mapping{
			ID:         id,
			Host:       body.Host,
			RemotePath: body.RemotePath,
			LocalPath:  body.LocalPath,
		}
		updated, err := svc.Update(r.Context(), &model)
		if errors.Is(err, remotepathmappings.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("remotepathmapping %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("remotepathmappings: %w", err), nil)
			return
		}
		writeRemotePathMappingJSON(w, http.StatusAccepted, *updated)
	})
}

// RemotePathMappingDeleteHandler returns DELETE /api/v3/remotepathmapping/{id}.
// Mirrors `DeleteMapping` at `:70-74`. 200 empty / 404.
func RemotePathMappingDeleteHandler(svc RemotePathMappingWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseRemotePathMappingID(w, r)
		if !ok {
			return
		}
		// Idempotent DELETE — C.1.i-follow-4 alignment (J232). C# returns 200 on missing id.
		if err := svc.Remove(r.Context(), id); err != nil && !errors.Is(err, remotepathmappings.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("remotepathmappings: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// ---------- helpers ----------

func parseRemotePathMappingID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "remotepathmapping: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("remotepathmapping: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeRemotePathMappingBody(r *http.Request) (*RemotePathMappingResource, error) {
	var t RemotePathMappingResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateRemotePathMapping ports the C# SharedValidator chain at
// `RemotePathMappingController.cs:24-46`:
//
//   - Host NotEmpty
//   - RemotePath NotEmpty + must not start or end with a space
//     (because remote paths cross OS boundaries, the C# IsValidPath
//     rule does NOT apply — only the spaces check)
//   - LocalPath IsValidPath (absolute) + not equal to "/"
//   - LocalPath SetValidator chain (mappedNetworkDrive, pathExists,
//     systemFolder) DEFERRED — those run filesystem-state checks
//     that need a disk adapter; the *remotepathmappings.Service.Add
//     path doesn't surface them, so the gap is observable only
//     on cross-OS test setups (rare for the empty-DB contract).
func validateRemotePathMapping(res *RemotePathMappingResource) *validation.Errors {
	errs := validation.New()
	if !errs.Required("Host", res.Host) {
		return errs
	}
	if !errs.Required("RemotePath", res.RemotePath) {
		return errs
	}
	if strings.HasPrefix(res.RemotePath, " ") {
		errs.Add("RemotePath", fmt.Sprintf("Remote Path '%s' must not start with a space", res.RemotePath))
	}
	if strings.HasSuffix(res.RemotePath, " ") {
		errs.Add("RemotePath", fmt.Sprintf("Remote Path '%s' must not end with a space", res.RemotePath))
	}
	if !errs.Required("LocalPath", res.LocalPath) {
		return errs
	}
	if !filepath.IsAbs(res.LocalPath) {
		errs.Add("LocalPath", "Local Path must be absolute")
	}
	if res.LocalPath == "/" {
		errs.Add("LocalPath", "Cannot be set to '/'")
	}
	return errs
}

func writeRemotePathMappingJSON(w http.ResponseWriter, status int, m remotepathmappings.Mapping) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(RemotePathMappingResource{
		ID:         m.ID,
		Host:       m.Host,
		RemotePath: m.RemotePath,
		LocalPath:  m.LocalPath,
	})
	_, _ = w.Write(buf)
}
