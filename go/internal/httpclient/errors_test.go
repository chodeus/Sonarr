package httpclient

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPError_DefaultMessage(t *testing.T) {
	req := mustReq(t, "https://example.com/api")
	req.Method = http.MethodPost
	resp := NewResponseFromBytes(req, NewHeader(), nil, http.StatusNotFound, "1.1")
	e := NewHTTPError(req, resp)
	want := "HTTP request failed: [404:NotFound] [POST] at [https://example.com/api]"
	if e.Error() != want {
		t.Errorf("got %q want %q", e.Error(), want)
	}
}

func TestHTTPError_AppendsBodyWhenPresent(t *testing.T) {
	req := mustReq(t, "https://example.com/api")
	h := NewHeader()
	h.SetContentType("application/json")
	resp := NewResponseFromString(req, h, `{"err":1}`, http.StatusBadRequest, "1.1")
	e := NewHTTPError(req, resp)
	if !strings.Contains(e.Error(), `{"err":1}`) {
		t.Errorf("expected body in Error(): %q", e.Error())
	}
}

func TestTooManyRequestsError_RetryAfterSeconds(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	h.Add("Retry-After", "30")
	resp := NewResponseFromBytes(req, h, nil, http.StatusTooManyRequests, "1.1")
	e := NewTooManyRequestsError(req, resp)
	if e.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v want 30s", e.RetryAfter)
	}
}

func TestTooManyRequestsError_RetryAfterDate(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	future := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC1123)
	h.Add("Retry-After", future)
	resp := NewResponseFromBytes(req, h, nil, http.StatusTooManyRequests, "1.1")
	e := NewTooManyRequestsError(req, resp)
	if e.RetryAfter < 30*time.Second || e.RetryAfter > 3*time.Minute {
		t.Errorf("RetryAfter from RFC1123 date out of range: %v", e.RetryAfter)
	}
}

func TestTooManyRequestsError_RetryAfterMissing(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	resp := NewResponseFromBytes(req, NewHeader(), nil, http.StatusTooManyRequests, "1.1")
	e := NewTooManyRequestsError(req, resp)
	if e.RetryAfter != 0 {
		t.Errorf("expected zero RetryAfter, got %v", e.RetryAfter)
	}
}

func TestAsHTTPError(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	resp := NewResponseFromBytes(req, NewHeader(), nil, 500, "1.1")
	e := NewHTTPError(req, resp)

	// Wrap once
	wrapped := errors.Join(errors.New("ctx"), e)
	got, ok := AsHTTPError(wrapped)
	if !ok || got != e {
		t.Errorf("AsHTTPError did not unwrap")
	}
}

func TestUnexpectedHTMLContentError(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	h.SetContentType("text/html")
	resp := NewResponseFromString(req, h, "<html>oops</html>", 200, "1.1")
	e := NewUnexpectedHTMLContentError(resp)
	if !strings.Contains(e.Error(), "Site responded with HTML content") {
		t.Errorf("error text: %q", e.Error())
	}
}
