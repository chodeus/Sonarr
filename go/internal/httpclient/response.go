package httpclient

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Response is the Sonarr-flavoured HTTP response DTO. C# cite:
// src/NzbDrone.Common/Http/HttpResponse.cs:10-99.
//
// The C# class has two constructors: one that takes raw bytes and lazy-decodes
// to string on first Content access, one that takes the string directly and
// pre-encodes the bytes. The Go port collapses to a single struct and exposes
// the same Content() lazy-decoder via a once.
type Response struct {
	Request      *Request // The originating request.
	Headers      *Header  // Response headers (always non-nil).
	StatusCode   int      // HTTP status (200, 404, etc.).
	HTTPVersion  string   // E.g. "HTTP/1.1".
	ResponseData []byte   // Raw body bytes.

	contentCache *string // Memoised string-decoded body. nil = not decoded yet.
}

// NewResponseFromBytes constructs a Response with raw body bytes. Mirrors
// HttpResponse(HttpRequest, HttpHeader, byte[], HttpStatusCode, Version) at
// HttpResponse.cs:14-21.
func NewResponseFromBytes(req *Request, headers *Header, body []byte, status int, httpVersion string) *Response {
	return &Response{
		Request:      req,
		Headers:      headers,
		StatusCode:   status,
		HTTPVersion:  httpVersion,
		ResponseData: body,
	}
}

// NewResponseFromString constructs a Response with a pre-decoded string body
// — the bytes are encoded back via the current Content-Type charset for
// callers that read ResponseData. Mirrors HttpResponse.cs:23-31.
func NewResponseFromString(req *Request, headers *Header, content string, status int, httpVersion string) *Response {
	enc := headers.EncodingFromContentType()
	var data []byte
	if enc != nil {
		if encoded, err := enc.NewEncoder().Bytes([]byte(content)); err == nil {
			data = encoded
		} else {
			data = []byte(content)
		}
	} else {
		data = []byte(content)
	}
	cached := content
	return &Response{
		Request:      req,
		Headers:      headers,
		StatusCode:   status,
		HTTPVersion:  httpVersion,
		ResponseData: data,
		contentCache: &cached,
	}
}

// Content returns the response body decoded to string per the Content-Type
// charset. Mirrors the lazy-decoded `Content` property at HttpResponse.cs:41-52.
func (r *Response) Content() string {
	if r.contentCache != nil {
		return *r.contentCache
	}
	enc := r.Headers.EncodingFromContentType()
	var s string
	if enc != nil {
		if decoded, err := enc.NewDecoder().Bytes(r.ResponseData); err == nil {
			s = string(decoded)
		} else {
			s = string(r.ResponseData)
		}
	} else {
		s = string(r.ResponseData)
	}
	r.contentCache = &s
	return s
}

// HasHTTPError reports whether the status code is >=400. C# cite:
// HttpResponse.cs:54.
func (r *Response) HasHTTPError() bool { return r.StatusCode >= 400 }

// HasHTTPServerError reports whether the status code is >=500. C# cite:
// HttpResponse.cs:56.
func (r *Response) HasHTTPServerError() bool { return r.StatusCode >= 500 }

// HasHTTPRedirect reports whether the status code is one of the redirect
// statuses Sonarr's client follows. C# cite: HttpResponse.cs:58-64.
//
// Note: the C# enum values are 301 Moved (alias 301 MovedPermanently),
// 302 Found, 303 SeeOther, 304 nope, 307 TemporaryRedirect, 308 PermanentRedirect,
// plus the ambiguous 300 RedirectMethod (which in C# is Redirect = 302). The
// canonical port is 301/302/303/307/308.
func (r *Response) HasHTTPRedirect() bool {
	switch r.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// reCookieKV mirrors RegexSetCookie at HttpResponse.cs:12.
var reCookieKV = regexp.MustCompile(`^(.*?)=(.*?)(?:;|$)`)

// CookieHeaders returns the raw Set-Cookie headers from the response. Mirrors
// HttpResponse.GetCookieHeaders at HttpResponse.cs:66-69.
func (r *Response) CookieHeaders() []string {
	v := r.Headers.Values("Set-Cookie")
	if v == nil {
		return nil
	}
	return v
}

// Cookies returns the parsed Set-Cookie name→value map. Mirrors
// HttpResponse.GetCookies at HttpResponse.cs:71-86.
func (r *Response) Cookies() map[string]string {
	out := map[string]string{}
	for _, h := range r.CookieHeaders() {
		m := reCookieKV.FindStringSubmatch(h)
		if m != nil {
			out[m[1]] = m[2]
		}
	}
	return out
}

// String produces the same shape as HttpResponse.ToString at
// HttpResponse.cs:88-98: `Res: HTTP/{Version} [{Method}] {Url}: {code}.{name} ({n} bytes)`.
// On error responses with a non-HTML content type, the body is appended.
func (r *Response) String() string {
	bodyLen := len(r.ResponseData)
	out := fmt.Sprintf("Res: HTTP/%s [%s] %s: %d.%s (%d bytes)",
		r.HTTPVersion,
		r.Request.Method,
		r.Request.URL.String(),
		r.StatusCode,
		http.StatusText(r.StatusCode),
		bodyLen,
	)
	if r.HasHTTPError() {
		ct := r.Headers.ContentType()
		if strings.TrimSpace(ct) != "" && !strings.EqualFold(ct, "text/html") {
			out += "\n" + r.Content()
		}
	}
	return out
}
