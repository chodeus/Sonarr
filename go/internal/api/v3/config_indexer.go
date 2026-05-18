package v3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
)

// IndexerConfigResource mirrors
// `src/Sonarr.Api.V3/Config/IndexerConfigResource.cs`. Five fields,
// all int — the inherited Id always emits 1 (singleton).
//
// Field-source mapping (C# → Go):
//
//	id              → constant 1 (RestResource singleton convention)
//	minimumAge      → config.Provider.MinimumAge
//	retention       → config.Provider.Retention
//	maximumSize     → config.Provider.MaximumSize
//	rssSyncInterval → config.Provider.RssSyncInterval
type IndexerConfigResource struct {
	ID              int `json:"id"`
	MinimumAge      int `json:"minimumAge"`
	Retention       int `json:"retention"`
	MaximumSize     int `json:"maximumSize"`
	RssSyncInterval int `json:"rssSyncInterval"`
}

// IndexerConfigGetter is the read side the handler depends on. The
// config.Provider satisfies it directly via its existing getter
// methods — no interface adapter needed since the methods are
// trivially typed.
type IndexerConfigGetter interface {
	MinimumAge() int
	Retention() int
	MaximumSize() int
	RssSyncInterval() int
}

// IndexerConfigHandler returns the http.Handler for
// GET /api/v3/config/indexer. Mirrors `IndexerConfigController` —
// the C# RestController<T>.GetResourceById(1) one-call-site
// indirection collapses into a direct getter call.
func IndexerConfigHandler(getter IndexerConfigGetter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		res := IndexerConfigResource{
			ID:              1,
			MinimumAge:      getter.MinimumAge(),
			Retention:       getter.Retention(),
			MaximumSize:     getter.MaximumSize(),
			RssSyncInterval: getter.RssSyncInterval(),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(res)
		_, _ = w.Write(buf)
	})
}

// ---------- 5.B13: PUT ----------

// IndexerConfigWriter is the write surface — per-field setters that
// the config.Provider satisfies natively (one Set method per JSON
// field). Mirrors the C# ConfigController.SaveConfig reflection-
// over-properties pattern as explicit per-field calls.
type IndexerConfigWriter interface {
	IndexerConfigGetter
	SetMinimumAge(ctx context.Context, v int) error
	SetRetention(ctx context.Context, v int) error
	SetMaximumSize(ctx context.Context, v int) error
	SetRssSyncInterval(ctx context.Context, v int) error
}

// IndexerConfigUpdateHandler returns PUT /api/v3/config/indexer/{id}.
// Mirrors `IndexerConfigController` (inherits from `ConfigController
// <IndexerConfigResource>` at
// `src/Sonarr.Api.V3/Config/IndexerConfigController.cs` / `:35`
// from the base). Path id ignored; body Id wins. Returns 202 + body.
//
// Validation chain ports the C# `IndexerConfigController` rules at
// `src/Sonarr.Api.V3/Config/IndexerConfigController.cs:18-21`:
//
//   - MinimumAge >= 0
//   - Retention >= 0
//   - MaximumSize >= 0
//   - RssSyncInterval in {0} ∪ [10, 120] (RssSyncInterval = 0 disables RSS)
func IndexerConfigUpdateHandler(svc IndexerConfigWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body IndexerConfigResource
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("config/indexer: malformed body: %v", err))
			return
		}
		if errs := validateIndexerConfig(&body); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		ctx := r.Context()
		if err := svc.SetMinimumAge(ctx, body.MinimumAge); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/indexer: %w", err), nil)
			return
		}
		if err := svc.SetRetention(ctx, body.Retention); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/indexer: %w", err), nil)
			return
		}
		if err := svc.SetMaximumSize(ctx, body.MaximumSize); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/indexer: %w", err), nil)
			return
		}
		if err := svc.SetRssSyncInterval(ctx, body.RssSyncInterval); err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("config/indexer: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		buf, _ := json.Marshal(IndexerConfigResource{
			ID:              1,
			MinimumAge:      svc.MinimumAge(),
			Retention:       svc.Retention(),
			MaximumSize:     svc.MaximumSize(),
			RssSyncInterval: svc.RssSyncInterval(),
		})
		_, _ = w.Write(buf)
	})
}

func validateIndexerConfig(res *IndexerConfigResource) *validation.Errors {
	errs := validation.New()
	errs.NonNegative("MinimumAge", res.MinimumAge)
	errs.NonNegative("Retention", res.Retention)
	errs.NonNegative("MaximumSize", res.MaximumSize)
	// RssSyncInterval: 0 disables RSS; otherwise [10, 120] is the
	// allowed range per the C# `.Between(10, 120)` validator with
	// the .When() disable-zero exception.
	if res.RssSyncInterval != 0 {
		errs.Range("RssSyncInterval", res.RssSyncInterval, 10, 120)
	}
	return errs
}
