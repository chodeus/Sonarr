package host

import (
	"encoding/json"
	"net/http"
)

// APIInfoResource is the response body for GET /api. Mirrors
// `src/Sonarr.Http/ApiInfoResource.cs`. The values are constants;
// C# pins them in `ApiInfoController.GetApiInfo`
// (`src/Sonarr.Http/ApiInfoController.cs:13-16`).
//
// Field-source mapping (C# → Go):
//
//	current    → constant "v5"        (ApiInfoController.cs:14)
//	deprecated → constant ["v3"]       (ApiInfoController.cs:15)
type APIInfoResource struct {
	Current    string   `json:"current"`
	Deprecated []string `json:"deprecated"`
}

// apiInfoHandler serves GET /api behind the requireAPIKey middleware.
// C# does not mark the controller [AllowAnonymous]; the Startup
// fallback policy makes auth mandatory.
func (s *Server) apiInfoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(APIInfoResource{
			Current:    "v5",
			Deprecated: []string{"v3"},
		})
		_, _ = w.Write(buf)
	})
}
