package host

import (
	"context"
	"encoding/json"
	"net/http"
)

// ConfigProbe is the DB-liveness signal /ping uses. The C# controller
// (`src/Sonarr.Http/Ping/PingController.cs:31`) calls
// `_configRepository.All()` and wraps it in a 5-second cache; any
// exception flips the response to 500 + `{"status":"Error"}`.
//
// The Go interface narrows that contract to "did the probe succeed"
// since /ping does not need the rows themselves — only whether the
// query throws. The 5-second cache lives in a follow-up slice; this
// first slice probes on every request, matching the cache-miss path.
type ConfigProbe interface {
	All(ctx context.Context) error
}

// PingResource is the response body. Mirrors
// `src/Sonarr.Http/Ping/PingResource.cs`. C# emits the field as
// camelCase via STJson.ApplySerializerSettings (Startup.cs:107), so
// the Go tag matches.
//
// Field-source mapping (C# → Go):
//
//	status → "OK" on probe success, "Error" on probe failure
//	         (passthrough constant, never null in either implementation)
type PingResource struct {
	Status string `json:"status"`
}

func (s *Server) pingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		body := PingResource{Status: "OK"}
		if err := s.probe.All(r.Context()); err != nil {
			s.logger.Warn("ping probe failed", "err", err)
			status = http.StatusInternalServerError
			body = PingResource{Status: "Error"}
		}
		writePingJSON(w, r, status, body)
	})
}

// writePingJSON emits the C#-equivalent of
// `Produces("application/json")` + `StatusCode(...)`: `application/json;
// charset=utf-8` content type, the status code, and a pretty-printed
// body — except for HEAD where the body is suppressed per RFC 9110.
//
// Body shape is `{"status":"OK"}` (no trailing newline) to match
// System.Text.Json's default minified output. encoding/json's
// Encoder.Encode appends a newline; we use Marshal here to avoid
// that.
func writePingJSON(w http.ResponseWriter, r *http.Request, status int, body PingResource) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	buf, _ := json.Marshal(body)
	_, _ = w.Write(buf)
}
