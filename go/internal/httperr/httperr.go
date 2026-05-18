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
package httperr

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

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
