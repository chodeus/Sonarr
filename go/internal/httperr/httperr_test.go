package httperr

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrite_EmitsCSharpEnvelopeShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	Write(rr, http.StatusInternalServerError, "boom", "stack trace here", nil)
	if rr.Code != 500 {
		t.Errorf("status: got %d want 500", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q want application/json", ct)
	}
	var got Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Message != "boom" {
		t.Errorf("message: got %q want %q", got.Message, "boom")
	}
	if got.Description != "stack trace here" {
		t.Errorf("description: got %q want %q", got.Description, "stack trace here")
	}
	if got.Content != nil {
		t.Errorf("content: must be nil, got %v", got.Content)
	}
}

func TestWrite_OmitsNullDescriptionAndContent(t *testing.T) {
	t.Parallel()
	// C# `JsonIgnoreCondition.WhenWritingNull` drops description +
	// content when null. Our omitempty struct tags must do the same.
	rr := httptest.NewRecorder()
	Write(rr, http.StatusNotFound, "missing", "", nil)
	body := rr.Body.String()
	if strings.Contains(body, "description") {
		t.Errorf("body must omit description key when empty: %s", body)
	}
	if strings.Contains(body, "content") {
		t.Errorf("body must omit content key when nil: %s", body)
	}
	// Confirm shape is exactly the single-key form.
	if body != `{"message":"missing"}` {
		t.Errorf("body: got %q want %q", body, `{"message":"missing"}`)
	}
}

func TestWrite_EmitsContentWhenProvided(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	Write(rr, http.StatusBadRequest, "invalid", "", map[string]any{"field": "name"})
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	content, ok := got["content"].(map[string]any)
	if !ok {
		t.Fatalf("content key missing or wrong type: %v", got)
	}
	if content["field"] != "name" {
		t.Errorf("content.field: got %v want name", content["field"])
	}
}

func TestWriteValidation_EmitsArrayWithDefaultSeverityError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WriteValidation(rr, []Failure{
		{PropertyName: "Name", ErrorMessage: "is required"},
		{PropertyName: "Port", ErrorMessage: "must be in range 1-65535"},
	})
	if rr.Code != 400 {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type: got %q", ct)
	}
	var got []Failure
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("failures: got %d want 2", len(got))
	}
	for i, f := range got {
		if f.Severity != "error" {
			t.Errorf("failures[%d].severity: got %q want error (default)", i, f.Severity)
		}
	}
}

func TestWriteValidation_NilEmitsEmptyArray(t *testing.T) {
	t.Parallel()
	// C# emits `[]` (not omitted) for an empty validation collection.
	// Our nil→[]Failure{} normalisation must preserve that.
	rr := httptest.NewRecorder()
	WriteValidation(rr, nil)
	if rr.Body.String() != "[]" {
		t.Errorf("body: got %q want %q", rr.Body.String(), "[]")
	}
}

func TestWriteValidation_PreservesExplicitSeverity(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	WriteValidation(rr, []Failure{{PropertyName: "X", ErrorMessage: "warn", Severity: "warning"}})
	var got []Failure
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got[0].Severity != "warning" {
		t.Errorf("severity: got %q want warning", got[0].Severity)
	}
}

func TestWriteErr_LogsFiveHundredsOnly(t *testing.T) {
	t.Parallel()
	// 500 must hit the logger; 4xx must NOT (client errors are noisy).
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v3/oops", nil)
	WriteErr(rr, req, http.StatusInternalServerError, errors.New("db down"), logger)
	if !strings.Contains(buf.String(), "handler error") {
		t.Errorf("logger must capture 500 error; got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/api/v3/oops") {
		t.Errorf("logger must include path; got %q", buf.String())
	}

	buf.Reset()
	rr2 := httptest.NewRecorder()
	WriteErr(rr2, req, http.StatusNotFound, errors.New("missing"), logger)
	if buf.Len() != 0 {
		t.Errorf("logger must NOT log 4xx; got %q", buf.String())
	}
}

func TestWriteErr_NilLoggerSafe(t *testing.T) {
	t.Parallel()
	// Handlers should be able to pass a nil logger without panicking.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	WriteErr(rr, req, http.StatusInternalServerError, errors.New("x"), nil)
	if rr.Code != 500 {
		t.Errorf("status: got %d", rr.Code)
	}
}

func TestNotFoundConflictBadRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		call   func(w http.ResponseWriter)
		status int
	}{
		{"NotFound", func(w http.ResponseWriter) { NotFound(w, "missing") }, 404},
		{"Conflict", func(w http.ResponseWriter) { Conflict(w, "constraint failed") }, 409},
		{"BadRequest", func(w http.ResponseWriter) { BadRequest(w, "bad json") }, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.call(rr)
			if rr.Code != tt.status {
				t.Errorf("status: got %d want %d", rr.Code, tt.status)
			}
			if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("content-type: got %q", ct)
			}
			var got Envelope
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Message == "" {
				t.Errorf("message must be non-empty")
			}
		})
	}
}

func TestInternal_DelegatesToWriteErr(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v3/series", nil)
	Internal(rr, req, errors.New("db x"), logger)
	if rr.Code != 500 {
		t.Errorf("status: got %d", rr.Code)
	}
	if !strings.Contains(buf.String(), "POST") {
		t.Errorf("logger must include method; got %q", buf.String())
	}
}

func TestWithLogger_RoundTripsThroughContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	req := httptest.NewRequest("GET", "/", nil)
	req2 := WithLogger(req, logger)
	got := LoggerFromRequest(req2)
	if got != logger {
		t.Errorf("LoggerFromRequest: got %v want %v", got, logger)
	}
}

func TestWithLogger_NilLoggerIsNoOp(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest("GET", "/", nil)
	req2 := WithLogger(req, nil)
	if req2 != req {
		t.Errorf("WithLogger(req, nil) must return req unchanged")
	}
	if LoggerFromRequest(req2) != nil {
		t.Errorf("LoggerFromRequest must be nil when none injected")
	}
}

func TestLoggerFromRequest_NilRequestSafe(t *testing.T) {
	t.Parallel()
	if got := LoggerFromRequest(nil); got != nil {
		t.Errorf("LoggerFromRequest(nil): got %v want nil", got)
	}
}

func TestWriteErr_FallsBackToContextLogger(t *testing.T) {
	t.Parallel()
	// The 5.A5 middleware injects a logger via context; WriteErr
	// callers pass nil for the logger arg and the function picks it
	// up from the request. This pins the contract that closes the
	// "zero handler-side logging" gap end-to-end.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	req := httptest.NewRequest("POST", "/api/v3/series", nil)
	req = WithLogger(req, logger)
	rr := httptest.NewRecorder()
	WriteErr(rr, req, http.StatusInternalServerError, errors.New("ctx-logger"), nil)
	if !strings.Contains(buf.String(), "ctx-logger") {
		t.Errorf("context logger must capture 500: got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/api/v3/series") {
		t.Errorf("logger must include path: %q", buf.String())
	}
}

func TestWriteErr_ExplicitLoggerWinsOverContext(t *testing.T) {
	t.Parallel()
	// If a caller passes an explicit logger, it MUST be used in
	// preference to any context-injected logger. Tests typically
	// supply an explicit one to capture output.
	var ctxBuf, explicitBuf bytes.Buffer
	ctxLogger := slog.New(slog.NewTextHandler(&ctxBuf, nil))
	explicitLogger := slog.New(slog.NewTextHandler(&explicitBuf, nil))
	req := httptest.NewRequest("GET", "/", nil)
	req = WithLogger(req, ctxLogger)
	rr := httptest.NewRecorder()
	WriteErr(rr, req, http.StatusInternalServerError, errors.New("x"), explicitLogger)
	if ctxBuf.Len() != 0 {
		t.Errorf("context logger must NOT fire when explicit provided")
	}
	if explicitBuf.Len() == 0 {
		t.Errorf("explicit logger must fire")
	}
}

func TestWriteErr_EmittedShapeMatchesEnvelope(t *testing.T) {
	t.Parallel()
	// Verify that WriteErr's body is the same envelope shape Write
	// emits — so a client parsing one path can parse the other.
	rr := httptest.NewRecorder()
	WriteErr(rr, nil, http.StatusInternalServerError, errors.New("boom"), nil)
	var got Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Message != "boom" || got.Description != "boom" {
		t.Errorf("envelope mismatch: %+v", got)
	}
}
