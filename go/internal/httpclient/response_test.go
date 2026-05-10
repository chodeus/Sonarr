package httpclient

import (
	"net/http"
	"strings"
	"testing"
)

func mustReq(t *testing.T, raw string) *Request {
	t.Helper()
	r, err := NewRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResponse_StatusHelpers(t *testing.T) {
	cases := []struct {
		code                int
		err, sErr, redirect bool
	}{
		{200, false, false, false},
		{301, false, false, true},
		{302, false, false, true},
		{303, false, false, true},
		{307, false, false, true},
		{308, false, false, true},
		{304, false, false, false}, // NotModified is NOT a redirect we follow
		{400, true, false, false},
		{500, true, true, false},
	}
	req := mustReq(t, "https://example.com/")
	for _, c := range cases {
		r := NewResponseFromBytes(req, NewHeader(), nil, c.code, "1.1")
		if r.HasHTTPError() != c.err {
			t.Errorf("HasHTTPError(%d) = %v want %v", c.code, r.HasHTTPError(), c.err)
		}
		if r.HasHTTPServerError() != c.sErr {
			t.Errorf("HasHTTPServerError(%d) = %v want %v", c.code, r.HasHTTPServerError(), c.sErr)
		}
		if r.HasHTTPRedirect() != c.redirect {
			t.Errorf("HasHTTPRedirect(%d) = %v want %v", c.code, r.HasHTTPRedirect(), c.redirect)
		}
	}
}

func TestResponse_ContentLazyDecoded(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	h.SetContentType("text/plain; charset=utf-8")
	r := NewResponseFromBytes(req, h, []byte("hello"), 200, "1.1")
	if got := r.Content(); got != "hello" {
		t.Errorf("Content = %q", got)
	}
}

func TestResponse_FromString_PreCached(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	h.SetContentType("text/plain")
	r := NewResponseFromString(req, h, "world", 200, "1.1")
	if got := r.Content(); got != "world" {
		t.Errorf("Content = %q", got)
	}
}

func TestResponse_CookieParsers(t *testing.T) {
	req := mustReq(t, "https://example.com/")
	h := NewHeader()
	h.Add("Set-Cookie", "session=abc; Path=/")
	h.Add("Set-Cookie", "csrf=xyz123")
	r := NewResponseFromBytes(req, h, nil, 200, "1.1")
	got := r.Cookies()
	if got["session"] != "abc" {
		t.Errorf("session: %q", got["session"])
	}
	if got["csrf"] != "xyz123" {
		t.Errorf("csrf: %q", got["csrf"])
	}
}

func TestResponse_String_IncludesBodyOnNonHTMLError(t *testing.T) {
	req := mustReq(t, "https://example.com/foo")
	h := NewHeader()
	h.SetContentType("application/json")
	r := NewResponseFromString(req, h, `{"err":"oops"}`, http.StatusBadRequest, "1.1")
	s := r.String()
	if !strings.Contains(s, "Res: HTTP/1.1") {
		t.Errorf("missing prefix: %q", s)
	}
	if !strings.Contains(s, `{"err":"oops"}`) {
		t.Errorf("expected body in error string, got %q", s)
	}
}

func TestResponse_String_OmitsBodyOnHTMLError(t *testing.T) {
	req := mustReq(t, "https://example.com/foo")
	h := NewHeader()
	h.SetContentType("text/html")
	r := NewResponseFromString(req, h, "<html>oops</html>", http.StatusBadGateway, "1.1")
	s := r.String()
	if strings.Contains(s, "<html>") {
		t.Errorf("HTML body leaked into String(): %q", s)
	}
}
