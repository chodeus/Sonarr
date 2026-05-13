package v3

import (
	"encoding/json"
	"net/http"
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
