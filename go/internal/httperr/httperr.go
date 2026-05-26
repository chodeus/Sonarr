// Package httperr emits HTTP error responses in the wire shape that
// Sonarr's C# tree produces via `Sonarr.Http/ErrorManagement/ErrorModel.cs`
// and the FluentValidation pipeline at `SonarrErrorPipeline.cs`.
//
// Two shapes:
//
//  1. Non-validation envelope (5xx, 4xx other than 400):
//
//     {"message": "...", "description": "...", "content": {...}}
//
//     `content` and `description` are emitted only when non-null
//     (matches C# `JsonIgnoreCondition.WhenWritingNull` via STJson).
//     The C# `ErrorModel` class lives at
//     `src/Sonarr.Http/ErrorManagement/ErrorModel.cs`.
//
//  2. Validation array (400 only):
//
//     [{"propertyName": "...", "errorMessage": "...", "severity": "error"}, …]
//
//     This is the FluentValidation `ValidationFailure` projection.
//     The C# pipeline writes `STJson.ToJson(validationException.Errors)`
//     at `SonarrErrorPipeline.cs:52`. Properties match the FluentValidation
//     library class with `PropertyNamingPolicy.CamelCase` applied
//     (via STJson defaults at
//     `src/NzbDrone.Common/Serializer/System.Text.Json/STJson.cs:30`).
//
// Use [Write] / [WriteErr] for the non-validation case and
// [WriteValidation] for 400 validation responses. The package logs
// 500-class errors at slog.Error when given a non-nil logger; this
// closes the "zero handler-side logging" gap surfaced by the Phase 5
// audit.
//
// # Validation envelope policy (C.1.i-follow-2 — closed at J233)
//
// The J228 reference capture against live C# v5-develop `7c41dcbe2`
// surfaced FOUR distinct error-response shapes in C# Sonarr. The
// Go-port deliberately standardises on TWO shapes (the Envelope above
// and the Failure array). Per-path divergence rationale:
//
//   - **C# shape (1) — RFC 9110 problem-details** (`{type, title, status,
//     errors, traceId}`) emitted on (a) malformed JSON 400 and (b) true
//     404 from `[ApiController]` routes. This is ASP.NET Core's automatic
//     `ProblemDetailsFactory` middleware — framework-incidental, not
//     deliberate API design. **Go-port does NOT mirror.** Our `BadRequest`
//     and `NotFound` use the [Envelope] shape so consumers see one
//     consistent error format across the entire surface. Cost: tools that
//     parse RFC 9110 problem-details specifically (rare in the Sonarr
//     ecosystem) lose that path. Benefit: a single parser handles every
//     error response from Go-port.
//
//   - **C# shape (2) — bare `FluentValidation.ValidationFailure[]` array**
//     on field-validator 400s. This is the most consumer-facing shape
//     across the Sonarr surface and the one the React frontend's
//     validation-error renderer expects. **Go-port mirrors via
//     [WriteValidation].**
//
//   - **C# shape (3) — `{message, content}` legacy envelope** on a single
//     observed path (`v5.put.customformat-bulk.empty-ids` → 400 with
//     `{message: "BadRequest: ids must be provided", content: "ids must
//     be provided"}`). This is C#'s `BadRequestException.ToString()`
//     leakage — the "BadRequest: " prefix is an artifact of the Exception
//     subclass name, not deliberate. **Go-port does NOT mirror.** Our
//     [BadRequest] emits the standard [Envelope] without the prefix and
//     without the duplicated `content` field. Functionally equivalent
//     (the human-readable message is the same modulo prefix); structurally
//     consistent with the rest of the surface.
//
//   - **C# shape (4) — `{message, description}` with .NET stack trace**
//     on 500-class errors that bubble unhandled. Observed at
//     `post.customformat.empty-name` and `post.qualityprofile.duplicate`
//     in the J228 capture: these are upstream null-deref bugs in the
//     FluentValidation chain (the validator's `_formatService.All()` call
//     returns null when called during a Custom() rule, passing null to
//     `Enumerable.Any` which throws ArgumentNullException). The Go-port
//     handles nil correctly and returns clean 400 validation envelopes
//     for these inputs — **accidentally BETTER than C#.** Documented
//     deliberate divergence; do not "fix" by reproducing the upstream
//     null-deref.
//
// Reference samples for each shape (live C# v5.0.0.1658 against an
// empty-state install):
//
//	~/Code/GitHub/Sonarr-reference/v5-develop-7c41dcbe2/validation-envelopes/
//	  post.series.bad-json.json              C# shape (1) — malformed JSON 400
//	  get.series.404.json                    C# shape (1) — true 404
//	  post.series.empty-body.json            C# shape (2) — field validator 400
//	  v5.put.customformat-bulk.empty-ids.json  C# shape (3) — legacy envelope
//	  post.customformat.empty-name.json      C# shape (4) — upstream null-deref 500
//
// Future "Phase 6 shadow-traffic" comparison will surface these
// shape divergences as known-failing rows — they are deliberate, not
// bugs.
package httperr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// loggerCtxKey is the unexported context key for the per-request
// *slog.Logger injection used by the v3 handler error path.
type loggerCtxKey struct{}

// WithLogger returns a clone of r whose context carries logger.
// Used by the host's logger-injection middleware (slice 5.A5) so
// handlers can fetch the logger via [LoggerFromRequest] without
// every handler signature growing a *slog.Logger parameter.
//
// Nil logger is a no-op (returns r unchanged) so the middleware
// can wrap routes unconditionally even when no logger is wired
// (early-build / test contexts).
func WithLogger(r *http.Request, logger *slog.Logger) *http.Request {
	if logger == nil || r == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), loggerCtxKey{}, logger))
}

// LoggerFromRequest returns the *slog.Logger stashed by
// [WithLogger], or nil when no logger was injected. Callers should
// treat the nil case gracefully — for the v3 handler error path
// this means the slog.Error log line is silently skipped (matches
// the WriteErr nil-logger guard).
func LoggerFromRequest(r *http.Request) *slog.Logger {
	if r == nil {
		return nil
	}
	if v := r.Context().Value(loggerCtxKey{}); v != nil {
		if l, ok := v.(*slog.Logger); ok {
			return l
		}
	}
	return nil
}

// Envelope is the wire shape of a C# `ErrorModel` (non-validation
// errors — 5xx and 4xx other than 400). Fields are emitted only
// when non-empty to match C#'s `JsonIgnoreCondition.WhenWritingNull`.
type Envelope struct {
	Message     string `json:"message,omitempty"`
	Description string `json:"description,omitempty"`
	Content     any    `json:"content,omitempty"`
}

// Failure is one entry in the 400 validation-error array. Mirrors
// FluentValidation's `ValidationFailure` projected through STJson
// with camelCase property naming. The minimal three fields below
// cover every write-path validation site in C# Sonarr; richer
// FluentValidation fields (AttemptedValue, ErrorCode, CustomState)
// can be added when a test demands them.
type Failure struct {
	PropertyName string `json:"propertyName"`
	ErrorMessage string `json:"errorMessage"`
	Severity     string `json:"severity,omitempty"`
}

// Write emits the non-validation envelope with the given status,
// message, and optional description. content is the optional
// `Content` payload (any JSON-marshalable value); pass nil to omit.
//
// Content-Type is set to `application/json` to match the C# pipeline.
func Write(w http.ResponseWriter, status int, message, description string, content any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(Envelope{
		Message:     message,
		Description: description,
		Content:     content,
	})
	_, _ = w.Write(body)
}

// WriteErr is a convenience wrapper that flattens an `error` into
// the envelope: Message and Description both default to err.Error().
// This mirrors the C# pipeline default behaviour where
// `Message = ex.Message; Description = ex.ToString()` — Go's lack of
// per-error stack traces means we can't reproduce ToString() exactly,
// but err.Error() is the closest analog.
//
// If logger is non-nil and status >= 500, the error is logged at
// slog.Error along with the request method+path. This closes the
// "zero handler-side logging" gap from the Phase 5 audit; callers
// for 4xx errors should pass nil to skip logging (client errors are
// noisy and not actionable for the operator).
func WriteErr(w http.ResponseWriter, r *http.Request, status int, err error, logger *slog.Logger) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	// 5.A5 wires a logger-injection middleware at the host route
	// layer, so handlers that pass logger=nil still log via the
	// request-context fallback. This keeps the WriteErr call sites
	// in 5.A4's sweep terse (`httperr.WriteErr(..., nil)`) while
	// activating the slog.Error path once the middleware is in
	// place. Tests passing a non-nil logger still bypass the lookup.
	if logger == nil {
		logger = LoggerFromRequest(r)
	}
	if logger != nil && status >= 500 {
		method, path := "", ""
		if r != nil {
			method = r.Method
			path = r.URL.Path
		}
		logger.Error("handler error",
			"status", status,
			"method", method,
			"path", path,
			"err", err,
		)
	}
	Write(w, status, msg, msg, nil)
}

// WriteValidation emits a 400 Bad Request whose body is the
// FluentValidation-shaped JSON array of failures. The caller is
// responsible for collecting failures (validators not modelled in
// this package — see internal/validation for a Rule-style builder
// once it lands).
//
// A nil or empty failures slice still emits `[]` (matches the C#
// behaviour where an empty `validationException.Errors` collection
// serialises as `[]`, not omitted).
func WriteValidation(w http.ResponseWriter, failures []Failure) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	if failures == nil {
		failures = []Failure{}
	}
	for i := range failures {
		if failures[i].Severity == "" {
			failures[i].Severity = "error"
		}
	}
	body, _ := json.Marshal(failures)
	_, _ = w.Write(body)
}

// BadRequest emits 400 with the envelope shape (single-message
// errors that are NOT FluentValidation collections — e.g. malformed
// JSON body, missing query parameter). For per-field validation,
// use [WriteValidation] instead.
func BadRequest(w http.ResponseWriter, message string) {
	Write(w, http.StatusBadRequest, message, "", nil)
}

// NotFound emits 404 in the envelope shape. Mirrors the C# pipeline
// branch for `ModelNotFoundException`.
func NotFound(w http.ResponseWriter, message string) {
	Write(w, http.StatusNotFound, message, "", nil)
}

// Conflict emits 409 in the envelope shape. Mirrors the C# pipeline
// branch for `ModelConflictException` and the SQLite "constraint
// failed" path on PUT/POST.
func Conflict(w http.ResponseWriter, message string) {
	Write(w, http.StatusConflict, message, "", nil)
}

// Internal emits 500 in the envelope shape and logs the error at
// slog.Error if logger is non-nil. Convenience around WriteErr for
// the common case where a handler caught an unexpected error.
func Internal(w http.ResponseWriter, r *http.Request, err error, logger *slog.Logger) {
	WriteErr(w, r, http.StatusInternalServerError, err, logger)
}
