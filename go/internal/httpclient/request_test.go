package httpclient

import (
	"errors"
	"net/http"
	"testing"
)

func TestNewRequest_Defaults(t *testing.T) {
	r, err := NewRequest("https://example.com/api")
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != http.MethodGet {
		t.Errorf("Method = %q", r.Method)
	}
	if !r.AllowAutoRedirect {
		t.Error("AllowAutoRedirect default true expected")
	}
	if !r.StoreRequestCookie {
		t.Error("StoreRequestCookie default true expected")
	}
	if !r.LogHTTPError {
		t.Error("LogHTTPError default true expected")
	}
	if !r.ConnectionKeepAlive {
		t.Error("ConnectionKeepAlive default true expected")
	}
	if r.Cookies == nil {
		t.Error("Cookies map should be initialised")
	}
	if r.Headers == nil {
		t.Error("Headers should be initialised")
	}
}

func TestNewRequest_AcceptArg(t *testing.T) {
	r, err := NewRequest("https://example.com/", AcceptJSON)
	if err != nil {
		t.Fatal(err)
	}
	if r.Headers.Accept() != "application/json" {
		t.Errorf("Accept = %q", r.Headers.Accept())
	}
}

func TestRequest_String(t *testing.T) {
	r, _ := NewRequest("https://example.com/api")
	r.Method = http.MethodPost
	r.ContentSummary = "summary"
	got := r.String()
	want := "Req: [POST] https://example.com/api: summary"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRequest_Format_NoMethodNoSummary(t *testing.T) {
	r, _ := NewRequest("https://example.com/api")
	r.ContentSummary = "summary"
	got := r.Format(false, false)
	if got != "https://example.com/api" {
		t.Errorf("got %q", got)
	}
}

func TestRequest_SetContentString_DefaultsUTF8(t *testing.T) {
	r, _ := NewRequest("https://example.com/")
	if err := r.SetContentString("hello"); err != nil {
		t.Fatal(err)
	}
	if string(r.ContentData) != "hello" {
		t.Errorf("ContentData = %q", r.ContentData)
	}
}

func TestRequest_SetContentBytes(t *testing.T) {
	r, _ := NewRequest("https://example.com/")
	r.SetContentBytes([]byte{1, 2, 3})
	if len(r.ContentData) != 3 {
		t.Errorf("ContentData len %d", len(r.ContentData))
	}
}

func TestNopClient_AllMethodsReturnErrNotImplemented(t *testing.T) {
	c := NewNopClient()
	r, _ := NewRequest("https://example.com/")
	if _, err := c.Execute(t.Context(), r); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Execute: %v", err)
	}
	if _, err := c.Get(t.Context(), r); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Get: %v", err)
	}
	if _, err := c.Head(t.Context(), r); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Head: %v", err)
	}
	if _, err := c.Post(t.Context(), r); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Post: %v", err)
	}
	if err := c.DownloadFile(t.Context(), "u", "f"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("DownloadFile: %v", err)
	}
}

func TestInterceptorFunc_Defaults(t *testing.T) {
	var f InterceptorFunc
	r, _ := NewRequest("https://example.com/")
	if f.PreRequest(r) != r {
		t.Error("PreRequest with nil Pre should return arg")
	}
	resp := NewResponseFromBytes(r, NewHeader(), nil, 200, "1.1")
	if f.PostResponse(resp) != resp {
		t.Error("PostResponse with nil Post should return arg")
	}
}
