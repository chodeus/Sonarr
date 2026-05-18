package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
)

// ImportListConfigResource mirrors
// `src/Sonarr.Api.V3/Config/ImportListConfigResource.cs`. Two fields:
// the ListSyncLevel enum + the optional ListSyncTag tag id (only
// consulted when ListSyncLevel == "keepAndTag").
//
// The enum is camelCased at the wire boundary
// ("disabled"/"logOnly"/"keepAndUnmonitor"/"keepAndTag") and decoded
// back to its persisted PascalCase identifier by the setter chain.
type ImportListConfigResource struct {
	ID            int    `json:"id"`
	ListSyncLevel string `json:"listSyncLevel"`
	ListSyncTag   int    `json:"listSyncTag"`
}

// ImportListConfigGetter is the read surface for the GET handler.
// Satisfied by *config.Provider.
type ImportListConfigGetter interface {
	ListSyncLevel() string
	ListSyncTag() int
}

// ImportListConfigHandler returns GET /api/v3/config/importlist.
// Mirrors `ImportListConfigController` — singleton with Id=1.
func ImportListConfigHandler(getter ImportListConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := ImportListConfigResource{
			ID:            1,
			ListSyncLevel: CamelCaseEnumName(getter.ListSyncLevel()),
			ListSyncTag:   getter.ListSyncTag(),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ImportListConfigWriter is the write surface: getter + two setters.
type ImportListConfigWriter interface {
	ImportListConfigGetter
	SetListSyncLevel(ctx context.Context, v string) error
	SetListSyncTag(ctx context.Context, v int) error
}

// ListSyncLevelFromName reverses CamelCaseEnumName for the
// ListSyncLevelType enum.
func ListSyncLevelFromName(s string) (string, error) {
	switch s {
	case "disabled":
		return "Disabled", nil
	case "logOnly":
		return "LogOnly", nil
	case "keepAndUnmonitor":
		return "KeepAndUnmonitor", nil
	case "keepAndTag":
		return "KeepAndTag", nil
	}
	return "", fmt.Errorf("unknown listSyncLevel value %q (want disabled|logOnly|keepAndUnmonitor|keepAndTag)", s)
}

// ImportListConfigUpdateHandler returns PUT /api/v3/config/importlist/{id}.
// Mirrors `ImportListConfigController` (inherits ConfigController
// .SaveConfig). The C# SharedValidator at controller :16-19 rejects
// ListSyncTag <= 0 when ListSyncLevel == KeepAndTag with the verbatim
// message "Tag must be specified" — preserved here.
func ImportListConfigUpdateHandler(svc ImportListConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body ImportListConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/importlist: malformed body: %v", err))
			return
		}
		if errs := validateImportListConfig(&body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		level, err := ListSyncLevelFromName(body.ListSyncLevel)
		if err != nil {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "ListSyncLevel",
				ErrorMessage: err.Error(),
				Severity:     "error",
			}})
			return
		}
		ctx := r.Context()
		if err := svc.SetListSyncLevel(ctx, level); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/importlist: set ListSyncLevel: %w", err), nil)
			return
		}
		if err := svc.SetListSyncTag(ctx, body.ListSyncTag); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/importlist: set ListSyncTag: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(ImportListConfigResource{
			ID:            1,
			ListSyncLevel: CamelCaseEnumName(svc.ListSyncLevel()),
			ListSyncTag:   svc.ListSyncTag(),
		})
		_, _ = w.Write(buf)
	})
}

func validateImportListConfig(res *ImportListConfigResource) *validation.Errors {
	errs := validation.New()
	if res.ListSyncLevel == "keepAndTag" && res.ListSyncTag <= 0 {
		errs.Add("ListSyncTag", "Tag must be specified")
	}
	return errs
}
