package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/signalr"
)

// MetadataResource is the POST/PUT wire shape. Mirrors the C#
// `Sonarr.Api.V3.Metadata.MetadataResource` extending
// `ProviderResource<MetadataResource>` at
// `src/Sonarr.Api.V3/Metadata/MetadataResource.cs`. The READ path
// emits the richer [MetadataSchemaResource] (per-implementation
// FieldDef schema + populated values); the write path here accepts
// the minimal subset of fields the React UI actually sends back on
// POST/PUT. The response from the write handlers echoes the same
// shape so the UI's optimistic-update path round-trips cleanly.
//
// Metadata is the only provider family without per-row Tags or event
// toggles (the C# table has only 5 columns + Id). Tags is still on
// the wire (empty array) for cross-family parity but ignored by the
// from-resource projection.
type MetadataResource struct {
	ID                 int             `json:"id"`
	Name               string          `json:"name"`
	Implementation     string          `json:"implementation"`
	ImplementationName string          `json:"implementationName,omitempty"`
	ConfigContract     string          `json:"configContract"`
	Tags               []int           `json:"tags"`
	Enable             bool            `json:"enable"`
	Fields             []ProviderField `json:"fields,omitempty"`
}

// MetadataWriter is the CUD surface. *metadata.Repo satisfies via
// the embedded repo.Repository[Metadata].
type MetadataWriter interface {
	Get(ctx context.Context, id int) (*metadata.Metadata, error)
	All(ctx context.Context) ([]metadata.Metadata, error)
	Insert(ctx context.Context, n *metadata.Metadata) (*metadata.Metadata, error)
	Update(ctx context.Context, n *metadata.Metadata) error
	Delete(ctx context.Context, id int) error
}

// MetadataEmitter mirrors the
// *signalr.ResourceBroadcaster[MetadataResource] subset the write
// handlers call.
type MetadataEmitter interface {
	Created(ctx context.Context, resource MetadataResource) error
	Updated(ctx context.Context, resource MetadataResource) error
	Deleted(ctx context.Context, resource MetadataResource) error
}

// MetadataCreateHandler returns POST /api/v3/metadata. Mirrors
// `ProviderControllerBase.CreateProvider` at C# :81-93.
//
// Validation chain: Name + Implementation + ConfigContract non-empty
// + name unique. Registry-membership check deferred to 5.C5b's test
// endpoint.
func MetadataCreateHandler(svc MetadataWriter, emitter MetadataEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeMetadataBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: malformed body: %v", err))
			return
		}
		if failures := validateMetadataResource(r.Context(), body, 0, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromMetadataResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: invalid fields: %v", err))
			return
		}
		saved, err := svc.Insert(r.Context(), row)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: insert: %w", err), nil)
			return
		}
		res := toMetadataResource(*saved, body.Fields)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "metadata", "created", func() error {
			return emitter.Created(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// MetadataUpdateHandler returns PUT /api/v3/metadata/{id}.
func MetadataUpdateHandler(svc MetadataWriter, emitter MetadataEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "metadata: invalid id")
			return
		}
		body, err := decodeMetadataBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: malformed body: %v", err))
			return
		}
		existing, err := svc.Get(r.Context(), id)
		if err != nil && !errors.Is(err, metadata.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: get %d: %w", id, err), nil)
			return
		}
		if errors.Is(err, metadata.ErrNotFound) {
			if body.ID > 0 && body.ID != id {
				if e2, e := svc.Get(r.Context(), body.ID); e == nil {
					existing = e2
					id = body.ID
				}
			}
		}
		if existing == nil {
			httperr.NotFound(w, fmt.Sprintf("metadata %d not found", id))
			return
		}
		if failures := validateMetadataResource(r.Context(), body, id, svc); len(failures) > 0 {
			httperr.WriteValidation(w, failures)
			return
		}
		row, err := fromMetadataResource(body, existing)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: invalid fields: %v", err))
			return
		}
		row.ID = id
		if err := svc.Update(r.Context(), row); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: update %d: %w", id, err), nil)
			return
		}
		res := toMetadataResource(*row, body.Fields)
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "metadata", "updated", func() error {
			return emitter.Updated(r.Context(), res)
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// MetadataTester invokes a per-row provider Test. The cmd-side
// adapter wires this against metadata.Registry.Construct +
// metadata.Provider.Test under host-supplied metadata.Dependencies.
//
// Production Test works TODAY for all 4 concrete writers
// (XbmcMetadata / PlexMetadata / RoksboxMetadata / WdtvMetadata)
// per Phase 4 work — metadata is the only family where every
// schema-listed provider has a real implementation.
type MetadataTester interface {
	Test(ctx context.Context, n *metadata.Metadata) ([]metadata.TestFailure, error)
}

// MetadataTestHandler returns POST /api/v3/metadata/test. Same shape
// as the 5.C1b/5.C2b/5.C3b/5.C4b twins: 200 `{}` on success, 400
// FluentValidation array on failures, 400 single-Implementation
// failure on ErrNotRegistered, 500 on transport errors.
//
// SharedValidator skipped (matches C# `[SkipValidation(true,false)]`).
func MetadataTestHandler(tester MetadataTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeMetadataBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: malformed body: %v", err))
			return
		}
		row, err := fromMetadataResource(body, nil)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("metadata: invalid fields: %v", err))
			return
		}
		failures, err := tester.Test(r.Context(), row)
		if err != nil {
			if errors.Is(err, metadata.ErrNotRegistered) {
				httperr.WriteValidation(w, []httperr.Failure{{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q (configContract %q)", body.Implementation, body.ConfigContract),
					Severity:     "error",
				}})
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: test: %w", err), nil)
			return
		}
		if len(failures) > 0 {
			httperr.WriteValidation(w, metadataTestFailuresToFluentArray(failures))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// MetadataTestAllHandler returns POST /api/v3/metadata/testall.
// Mirrors C# ProviderControllerBase.TestAll at :233-257 and the
// 5.C1c/5.C2c/5.C3c/5.C4c twins. Reuses MetadataRowsLister
// (defined in metadata_schema.go for the GET list path) and the
// family-agnostic ProviderTestAllResult.
//
// Returns 400 if any row failed, 200 if all passed. Empty list
// returns [].
func MetadataTestAllHandler(lister MetadataRowsLister, tester MetadataTester) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata/testall: list: %w", err), nil)
			return
		}
		results := make([]ProviderTestAllResult, 0, len(rows))
		anyInvalid := false
		for _, row := range rows {
			row := row
			failures, err := tester.Test(r.Context(), &row)
			projected := make([]httperr.Failure, 0)
			switch {
			case errors.Is(err, metadata.ErrNotRegistered):
				projected = append(projected, httperr.Failure{
					PropertyName: "Implementation",
					ErrorMessage: fmt.Sprintf("Unknown implementation %q", row.Implementation),
					Severity:     "error",
				})
			case err != nil:
				projected = append(projected, httperr.Failure{
					PropertyName: "",
					ErrorMessage: fmt.Sprintf("Test failed: %v", err),
					Severity:     "error",
				})
			default:
				projected = append(projected, metadataTestFailuresToFluentArray(failures)...)
			}
			isValid := len(projected) == 0
			if !isValid {
				anyInvalid = true
			}
			results = append(results, ProviderTestAllResult{
				ID:                 row.ID,
				IsValid:            isValid,
				ValidationFailures: projected,
			})
		}
		status := http.StatusOK
		if anyInvalid {
			status = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		buf, _ := json.Marshal(results)
		_, _ = w.Write(buf)
	})
}

// metadataTestFailuresToFluentArray projects the family's TestFailure
// to httperr.Failure.
func metadataTestFailuresToFluentArray(failures []metadata.TestFailure) []httperr.Failure {
	out := make([]httperr.Failure, 0, len(failures))
	for _, f := range failures {
		out = append(out, httperr.Failure{
			PropertyName: f.PropertyName,
			ErrorMessage: f.ErrorMessage,
			Severity:     "error",
		})
	}
	return out
}

// MetadataDeleteHandler returns DELETE /api/v3/metadata/{id}.
func MetadataDeleteHandler(svc MetadataWriter, emitter MetadataEmitter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "metadata: invalid id")
			return
		}
		err = svc.Delete(r.Context(), id)
		if errors.Is(err, metadata.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("metadata %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("metadata: delete %d: %w", id, err), nil)
			return
		}
		signalr.FireAndLog(httperr.LoggerFromRequest(r), "metadata", "deleted", func() error {
			return emitter.Deleted(r.Context(), MetadataResource{ID: id})
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

func decodeMetadataBody(r *http.Request) (MetadataResource, error) {
	defer func() { _ = r.Body.Close() }()
	var out MetadataResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return MetadataResource{}, err
	}
	return out, nil
}

func validateMetadataResource(ctx context.Context, body MetadataResource, existingID int, svc MetadataWriter) []httperr.Failure {
	failures := make([]httperr.Failure, 0, 3)
	if strings.TrimSpace(body.Name) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Name", ErrorMessage: "'Name' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.Implementation) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "Implementation", ErrorMessage: "'Implementation' must not be empty.", Severity: "error"})
	}
	if strings.TrimSpace(body.ConfigContract) == "" {
		failures = append(failures, httperr.Failure{PropertyName: "ConfigContract", ErrorMessage: "'Config Contract' must not be empty.", Severity: "error"})
	}
	if body.Name != "" {
		rows, err := svc.All(ctx)
		if err == nil {
			for _, existing := range rows {
				if existing.ID == existingID {
					continue
				}
				if strings.EqualFold(existing.Name, body.Name) {
					failures = append(failures, httperr.Failure{PropertyName: "Name", ErrorMessage: "Should be unique", Severity: "error"})
					break
				}
			}
		}
	}
	return failures
}

// fromMetadataResource maps the wire body to a metadata.Metadata row.
// metadata.Metadata.Settings + ConfigContract are plain `string`
// (NOT NULL) — same as DownloadClient.
func fromMetadataResource(res MetadataResource, existing *metadata.Metadata) (*metadata.Metadata, error) {
	settings := "{}"
	if res.Fields != nil {
		s, err := settingsJSONFromFields(res.Fields)
		if err != nil {
			return nil, err
		}
		settings = s
	} else if existing != nil {
		settings = existing.Settings
	}
	return &metadata.Metadata{
		ID:             res.ID,
		Enable:         res.Enable,
		Name:           res.Name,
		Implementation: res.Implementation,
		Settings:       settings,
		ConfigContract: res.ConfigContract,
	}, nil
}

// toMetadataResource echoes the persisted row back in the write
// wire shape with the original Fields[] preserved (the React UI's
// optimistic-update path needs the same fields it sent back). Tags
// is always empty []int — metadata has no per-row Tags column.
func toMetadataResource(m metadata.Metadata, fields []ProviderField) MetadataResource {
	if fields == nil {
		fields = []ProviderField{}
	}
	return MetadataResource{
		ID:                 m.ID,
		Name:               m.Name,
		Implementation:     m.Implementation,
		ImplementationName: m.Implementation,
		ConfigContract:     m.ConfigContract,
		Tags:               []int{},
		Enable:             m.Enable,
		Fields:             fields,
	}
}
