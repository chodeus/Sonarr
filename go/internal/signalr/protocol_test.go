package signalr_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/signalr"
)

func TestReadHandshake_AcceptsJSONV1(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"protocol":"json","version":1}` + string(signalr.RecordSeparator))
	h, err := signalr.ReadHandshake(r)
	if err != nil {
		t.Fatalf("ReadHandshake: %v", err)
	}
	if h.Protocol != "json" || h.Version != 1 {
		t.Errorf("handshake: %+v", h)
	}
}

func TestReadHandshake_RejectsUnsupportedProtocol(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"protocol":"messagepack","version":1}` + string(signalr.RecordSeparator))
	_, err := signalr.ReadHandshake(r)
	if !errors.Is(err, signalr.ErrHandshakeUnsupported) {
		t.Errorf("got %v, want ErrHandshakeUnsupported", err)
	}
}

func TestReadHandshake_RejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"protocol":"json","version":2}` + string(signalr.RecordSeparator))
	_, err := signalr.ReadHandshake(r)
	if !errors.Is(err, signalr.ErrHandshakeUnsupported) {
		t.Errorf("got %v, want ErrHandshakeUnsupported", err)
	}
}

func TestReadHandshake_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`not-json` + string(signalr.RecordSeparator))
	_, err := signalr.ReadHandshake(r)
	if !errors.Is(err, signalr.ErrHandshakeMalformed) {
		t.Errorf("got %v, want ErrHandshakeMalformed", err)
	}
}

func TestReadHandshake_RejectsMissingTerminator(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"protocol":"json","version":1}`)
	_, err := signalr.ReadHandshake(r)
	if !errors.Is(err, signalr.ErrHandshakeMalformed) {
		t.Errorf("got %v, want ErrHandshakeMalformed", err)
	}
}

func TestWriteHandshakeResponse_EmptySuccess(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := signalr.WriteHandshakeResponse(&buf, ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := buf.Bytes()
	if len(got) < 1 || got[len(got)-1] != signalr.RecordSeparator {
		t.Errorf("missing RS terminator: %q", got)
	}
	stripped := got[:len(got)-1]
	if string(stripped) != `{}` {
		t.Errorf("success body should be `{}`: got %q", stripped)
	}
}

func TestWriteHandshakeResponse_ErrorEmitsField(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_ = signalr.WriteHandshakeResponse(&buf, "unsupported protocol")
	got := buf.String()
	if !strings.Contains(got, `"error":"unsupported protocol"`) {
		t.Errorf("error body: %q", got)
	}
}

func TestEncodeInvocation_WireShape(t *testing.T) {
	t.Parallel()
	type series struct {
		ID int `json:"id"`
	}
	body := signalr.NewResourceChange(series{ID: 1}, signalr.ActionUpdated)
	env := signalr.SignalRMessage{Name: "series", Body: body, Version: signalr.V3Version}
	out, err := signalr.EncodeInvocation(env)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Must end with RS.
	if out[len(out)-1] != signalr.RecordSeparator {
		t.Errorf("missing RS")
	}
	// Strip RS + parse to confirm the InvocationMessage shape.
	var inv signalr.InvocationMessage
	if err := json.Unmarshal(out[:len(out)-1], &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if inv.Type != signalr.MessageTypeInvocation {
		t.Errorf("type: %d", inv.Type)
	}
	if inv.Target != "receiveMessage" {
		t.Errorf("target: %q", inv.Target)
	}
	if len(inv.Arguments) != 1 {
		t.Fatalf("arguments len: %d", len(inv.Arguments))
	}
	// Argument 0 should round-trip back to an envelope-shaped object.
	argBytes, _ := json.Marshal(inv.Arguments[0])
	if !strings.Contains(string(argBytes), `"name":"series"`) {
		t.Errorf("argument 0: %s", argBytes)
	}
	if !strings.Contains(string(argBytes), `"version":3`) {
		t.Errorf("argument 0 version: %s", argBytes)
	}
}

func TestEncodePing(t *testing.T) {
	t.Parallel()
	out, err := signalr.EncodePing()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if out[len(out)-1] != signalr.RecordSeparator {
		t.Errorf("missing RS")
	}
	stripped := out[:len(out)-1]
	if string(stripped) != `{"type":6}` {
		t.Errorf("ping shape: %q", stripped)
	}
}

func TestReadFrame_StripsSeparator(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"a":1}` + string(signalr.RecordSeparator) + `{"b":2}` + string(signalr.RecordSeparator))
	got, err := signalr.ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("first frame: %q", got)
	}
	got, err = signalr.ReadFrame(r)
	if err != nil {
		t.Fatalf("ReadFrame second: %v", err)
	}
	if string(got) != `{"b":2}` {
		t.Errorf("second frame: %q", got)
	}
}

func TestReadFrame_EOFEmptyReturnsEOF(t *testing.T) {
	t.Parallel()
	r := strings.NewReader("")
	_, err := signalr.ReadFrame(r)
	if !errors.Is(err, io.EOF) {
		t.Errorf("got %v, want io.EOF", err)
	}
}

func TestReadFrame_EOFPartialReturnsUnexpected(t *testing.T) {
	t.Parallel()
	r := strings.NewReader(`{"partial"`)
	_, err := signalr.ReadFrame(r)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("got %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestSplitFrames(t *testing.T) {
	t.Parallel()
	rs := string(signalr.RecordSeparator)
	got := signalr.SplitFrames([]byte(`{"a":1}` + rs + `{"b":2}` + rs))
	if len(got) != 2 || string(got[0]) != `{"a":1}` || string(got[1]) != `{"b":2}` {
		t.Errorf("split: %+v", got)
	}
	if signalr.SplitFrames(nil) != nil {
		t.Errorf("nil input should return nil")
	}
}
