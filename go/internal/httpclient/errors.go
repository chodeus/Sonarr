package httpclient

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HTTPError is the typed error the client returns when the upstream replied
// with a non-success status (>=400) and the request did not opt into
// SuppressHTTPError. C# cite: src/NzbDrone.Common/Http/HttpException.cs:5-37.
//
// Mirrors the ToString() shape: includes the body content on error responses
// when the response is non-nil.
type HTTPError struct {
	Request  *Request
	Response *Response
	Message  string
}

// NewHTTPError builds an HTTPError with the C# default message format
// at HttpException.cs:18: `HTTP request failed: [status:name] [METHOD] at [url]`.
func NewHTTPError(req *Request, resp *Response) *HTTPError {
	msg := fmt.Sprintf("HTTP request failed: [%d:%s] [%s] at [%s]",
		resp.StatusCode,
		statusName(resp.StatusCode),
		req.Method,
		req.URL.String(),
	)
	return &HTTPError{Request: req, Response: resp, Message: msg}
}

// NewHTTPErrorWithMessage carries a custom message. Mirrors HttpException.cs:10-15.
func NewHTTPErrorWithMessage(req *Request, resp *Response, message string) *HTTPError {
	return &HTTPError{Request: req, Response: resp, Message: message}
}

// Error implements error. Mirrors HttpException.ToString() at HttpException.cs:27-35.
func (e *HTTPError) Error() string {
	if e.Response != nil && e.Response.ResponseData != nil {
		return e.Message + "\n" + e.Response.Content()
	}
	return e.Message
}

// statusName returns a short uppercase token resembling the C# HttpStatusCode
// enum name (e.g. 404 → "NotFound"). It is best-effort: callers that need
// the exact .NET token should not rely on this for parsing — only for logging.
func statusName(code int) string {
	if name, ok := statusNameTable[code]; ok {
		return name
	}
	return fmt.Sprintf("Status%d", code)
}

var statusNameTable = map[int]string{
	200: "OK",
	201: "Created",
	202: "Accepted",
	204: "NoContent",
	301: "MovedPermanently",
	302: "Found",
	303: "SeeOther",
	304: "NotModified",
	307: "TemporaryRedirect",
	308: "PermanentRedirect",
	400: "BadRequest",
	401: "Unauthorized",
	403: "Forbidden",
	404: "NotFound",
	405: "MethodNotAllowed",
	408: "RequestTimeout",
	409: "Conflict",
	410: "Gone",
	413: "PayloadTooLarge",
	418: "ImATeapot",
	422: "UnprocessableEntity",
	429: "TooManyRequests",
	500: "InternalServerError",
	502: "BadGateway",
	503: "ServiceUnavailable",
	504: "GatewayTimeout",
}

// TooManyRequestsError extends HTTPError with the parsed Retry-After. C# cite:
// src/NzbDrone.Common/Http/TooManyRequestsException.cs:5-27.
//
// The Retry-After header is parsed two ways:
//  1. Integer seconds → time.Duration.
//  2. RFC1123 date → delta from time.Now() converted to UTC.
//
// If both fail (or the header is absent), RetryAfter is the zero duration.
type TooManyRequestsError struct {
	*HTTPError
	RetryAfter time.Duration
}

// NewTooManyRequestsError builds the typed 429 error.
func NewTooManyRequestsError(req *Request, resp *Response) *TooManyRequestsError {
	base := NewHTTPError(req, resp)
	out := &TooManyRequestsError{HTTPError: base}
	if !resp.Headers.ContainsKey("Retry-After") {
		return out
	}
	raw := resp.Headers.Get("Retry-After")
	if seconds, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		out.RetryAfter = time.Duration(seconds) * time.Second
		return out
	}
	if t, err := ParseHTTPDate(raw); err == nil {
		d := t.Sub(time.Now().UTC())
		if d > 0 {
			out.RetryAfter = d
		}
	}
	return out
}

// UnexpectedHTMLContentError is returned by typed Get/Post helpers when the
// upstream returned text/html where JSON was expected. C# cite:
// src/NzbDrone.Common/Http/UnexpectedHtmlContentException.cs.
type UnexpectedHTMLContentError struct {
	*HTTPError
}

// NewUnexpectedHTMLContentError wraps a Response.
func NewUnexpectedHTMLContentError(resp *Response) *UnexpectedHTMLContentError {
	return &UnexpectedHTMLContentError{
		HTTPError: NewHTTPErrorWithMessage(resp.Request, resp, "Site responded with HTML content."),
	}
}

// TLSFailureError is returned when the dispatcher fails the TLS handshake.
// C# cite: src/NzbDrone.Common/Http/TlsFailureException.cs. Body of error
// kept minimal because the dispatcher (sub-arc 1b) wraps the underlying
// crypto/tls error.
type TLSFailureError struct {
	URL string
	Err error
}

// Error implements error.
func (e *TLSFailureError) Error() string {
	return fmt.Sprintf("TLS handshake failed for %s: %v", e.URL, e.Err)
}

// Unwrap exposes the underlying error.
func (e *TLSFailureError) Unwrap() error { return e.Err }

// AsHTTPError reports whether err is an *HTTPError and returns it.
func AsHTTPError(err error) (*HTTPError, bool) {
	var e *HTTPError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// AsTooManyRequestsError reports whether err is a *TooManyRequestsError.
func AsTooManyRequestsError(err error) (*TooManyRequestsError, bool) {
	var e *TooManyRequestsError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
