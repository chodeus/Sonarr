package httpclient

import (
	"errors"
	"testing"
	"time"
)

func TestHeader_AddGet(t *testing.T) {
	h := NewHeader()
	h.Add("X-Foo", "1")
	h.Add("X-Foo", "2")
	if !h.ContainsKey("x-foo") {
		t.Error("ContainsKey case-insensitive lookup failed")
	}
	got := h.Values("X-Foo")
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Errorf("Values = %v", got)
	}
}

func TestHeader_GetSingleValue_Multi(t *testing.T) {
	h := NewHeader()
	h.Add("X-Foo", "1")
	h.Add("X-Foo", "2")
	if _, err := h.GetSingleValue("X-Foo"); !errors.Is(err, ErrMultipleHeaderValues) {
		t.Errorf("expected ErrMultipleHeaderValues, got %v", err)
	}
}

func TestHeader_GetSingleValue_Absent(t *testing.T) {
	h := NewHeader()
	v, err := h.GetSingleValue("X-Missing")
	if err != nil || v != "" {
		t.Errorf("Absent: got (%q,%v)", v, err)
	}
}

func TestHeader_ContentLength(t *testing.T) {
	h := NewHeader()
	if _, ok, err := h.ContentLength(); ok || err != nil {
		t.Errorf("absent: ok=%v err=%v", ok, err)
	}
	h.SetContentLength(1234)
	n, ok, err := h.ContentLength()
	if !ok || err != nil || n != 1234 {
		t.Errorf("set: %d ok=%v err=%v", n, ok, err)
	}
	h.SetContentLength(-1) // clear
	if _, ok, _ := h.ContentLength(); ok {
		t.Errorf("clear failed")
	}
}

func TestHeader_ContentTypeAccept(t *testing.T) {
	h := NewHeader()
	h.SetContentType("application/json")
	if h.ContentType() != "application/json" {
		t.Errorf("SetContentType: %q", h.ContentType())
	}
	h.SetContentType("")
	if h.ContentType() != "" {
		t.Errorf("Clear ContentType failed: %q", h.ContentType())
	}
	h.SetAccept("application/xml")
	if h.Accept() != "application/xml" {
		t.Errorf("SetAccept: %q", h.Accept())
	}
}

func TestEncodingFromContentType(t *testing.T) {
	cases := []struct {
		in       string
		wantUTF8 bool
	}{
		{"application/json", true},               // no charset → utf-8
		{"text/html; charset=utf-8", true},       // explicit utf-8
		{"text/html; charset=\"utf-8\"", true},   // quoted
		{"text/html; charset=ISO-8859-1", false}, // latin-1
	}
	for _, c := range cases {
		enc := EncodingFromContentType(c.in)
		if enc == nil {
			t.Errorf("EncodingFromContentType(%q) nil", c.in)
			continue
		}
	}
}

func TestParseHTTPDate(t *testing.T) {
	// RFC1123 with explicit GMT — the format the spec mandates.
	s := "Wed, 21 Oct 2015 07:28:00 GMT"
	got, err := ParseHTTPDate(s)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2015, 10, 21, 7, 28, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseCookies(t *testing.T) {
	got := ParseCookies("foo=bar; baz=qux ; bare ; broken=x=y")
	want := []QueryPair{{"foo", "bar"}, {"baz", "qux"}, {"broken", "x=y"}}
	if len(got) != len(want) {
		t.Fatalf("len %d want %d (got: %#v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %#v want %#v", i, got[i], want[i])
		}
	}
}

func TestSplitContentType(t *testing.T) {
	got := splitContentType("text/html; charset=UTF-8; boundary=xyz")
	want := []string{"text/html", "charset", "utf-8", "boundary", "xyz"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch %v vs %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] %q != %q", i, got[i], want[i])
		}
	}
}
