package signalr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// SignalR JSON Hub protocol — wire-format reference at
// https://github.com/dotnet/aspnetcore/blob/main/src/SignalR/docs/specs/HubProtocol.md
//
// Message framing: every frame is a single JSON object followed by
// an ASCII Record Separator (0x1E) byte. The WebSocket transport
// sends one frame per WS message; the long-polling transport
// concatenates multiple frames in a single HTTP body.
//
// This file implements the JSON-protocol layer in an I/O-agnostic
// way — pure encode/decode functions over `[]byte` so the eventual
// WebSocket transport (next slice) can plug in by pumping bytes
// through these helpers.

// RecordSeparator is the ASCII RS (0x1E) byte SignalR uses to
// terminate every JSON frame. The "control" choice avoids any
// JSON-internal byte collision — RS doesn't appear inside valid
// JSON.
const RecordSeparator byte = 0x1E

// MessageType mirrors the SignalR HubProtocol message-type enum at
// `HubProtocol.md` § "Message types". Only the variants the Sonarr
// hub uses are wired today; the rest are placeholders for spec
// completeness and may be implemented when a need surfaces.
type MessageType int

// MessageType enum values. Stable wire integers — never reorder.
const (
	// MessageTypeInvocation is what `Clients.All.SendAsync(method,
	// args)` becomes on the wire: a one-way method call from server
	// to client. The Sonarr hub uses this exclusively (target =
	// "receiveMessage", args = [SignalRMessage]).
	MessageTypeInvocation MessageType = 1
	// MessageTypeStreamItem — server pushes one item of an ongoing
	// stream. Sonarr's hub doesn't stream; unused today.
	MessageTypeStreamItem MessageType = 2
	// MessageTypeCompletion — pair to Invocation when the client
	// requested a return value. Sonarr's hub is fire-and-forget so
	// it never sends Completion frames.
	MessageTypeCompletion MessageType = 3
	// MessageTypeStreamInvocation — like Invocation but expects a
	// stream response.
	MessageTypeStreamInvocation MessageType = 4
	// MessageTypeCancelInvocation — client tells server to stop a
	// pending invocation.
	MessageTypeCancelInvocation MessageType = 5
	// MessageTypePing is the keep-alive frame. The server sends one
	// every `KeepAliveInterval` (default 15s) when otherwise idle;
	// the client does the same in the reverse direction.
	MessageTypePing MessageType = 6
	// MessageTypeClose — graceful close signal. Either side can
	// send.
	MessageTypeClose MessageType = 7
)

// ClientHandshake is the first frame a SignalR client sends after
// the WebSocket upgrade. The Sonarr hub only ever sees `{"protocol":
// "json","version":1}`. The server replies with HandshakeResponse;
// only then does normal message flow begin.
type ClientHandshake struct {
	Protocol string `json:"protocol"`
	Version  int    `json:"version"`
}

// HandshakeResponse is the server's reply to ClientHandshake.
// Empty body means success; `{"error": "<message>"}` means the
// negotiated protocol/version isn't supported. The Sonarr hub only
// accepts json/v1, so the success path emits `{}` and any other
// pairing emits an error.
type HandshakeResponse struct {
	Error string `json:"error,omitempty"`
}

// InvocationMessage is the wire shape of a server→client method
// call. Target is always "receiveMessage" for Sonarr's hub
// (`Clients.All.SendAsync("receiveMessage", message)`); Arguments
// carries a single-element array holding the SignalRMessage
// envelope.
//
// InvocationID is intentionally omitted — Sonarr's hub never
// expects a Completion reply, and the SignalR JS client treats a
// missing invocation-id as fire-and-forget.
type InvocationMessage struct {
	Type      MessageType `json:"type"`
	Target    string      `json:"target"`
	Arguments []any       `json:"arguments"`
}

// PingMessage is the bare keep-alive frame. Both directions emit
// it on idle ticks.
type PingMessage struct {
	Type MessageType `json:"type"`
}

// ErrHandshakeMalformed surfaces when the client's handshake frame
// can't be parsed (missing RecordSeparator, invalid JSON, missing
// protocol field). The transport should respond with a close
// frame in this case.
var ErrHandshakeMalformed = errors.New("signalr: client handshake malformed")

// ErrHandshakeUnsupported surfaces when the client requests a
// protocol/version the hub doesn't speak. The transport sends a
// HandshakeResponse with the error message, then closes.
var ErrHandshakeUnsupported = errors.New("signalr: client handshake protocol unsupported")

// parseHandshakeBytes decodes a ClientHandshake JSON object from
// bytes. Shared between the streaming ReadHandshake path and the
// WebSocket transport which receives a pre-framed message.
func parseHandshakeBytes(b []byte) (ClientHandshake, error) {
	var h ClientHandshake
	if err := json.Unmarshal(b, &h); err != nil {
		return ClientHandshake{}, fmt.Errorf("%w: %w", ErrHandshakeMalformed, err)
	}
	return h, nil
}

// ReadHandshake consumes the first RS-terminated frame from r,
// parses the ClientHandshake, and validates that the requested
// protocol/version is supported. Returns the parsed handshake on
// success; ErrHandshakeMalformed or ErrHandshakeUnsupported
// otherwise.
//
// The Sonarr hub only accepts protocol="json" + version=1 (the
// ASP.NET Core SignalR default). Future support for messagepack or
// json-v2 would extend the accept list here.
func ReadHandshake(r io.Reader) (ClientHandshake, error) {
	frame, err := ReadFrame(r)
	if err != nil {
		return ClientHandshake{}, fmt.Errorf("%w: %w", ErrHandshakeMalformed, err)
	}
	var h ClientHandshake
	if err := json.Unmarshal(frame, &h); err != nil {
		return ClientHandshake{}, fmt.Errorf("%w: %w", ErrHandshakeMalformed, err)
	}
	if h.Protocol != "json" || h.Version != 1 {
		return h, fmt.Errorf("%w: protocol=%q version=%d", ErrHandshakeUnsupported, h.Protocol, h.Version)
	}
	return h, nil
}

// WriteHandshakeResponse emits the server's HandshakeResponse —
// empty body for success, `{"error": "<message>"}` for rejection.
// Mirrors the ASP.NET Core SignalR handshake-completion flow at
// `HubProtocol.md` § "Handshake".
func WriteHandshakeResponse(w io.Writer, respErr string) error {
	resp := HandshakeResponse{Error: respErr}
	return WriteFrame(w, resp)
}

// EncodeInvocation builds the bytes for one Invocation frame
// (target="receiveMessage", args=[msg]). The frame is the JSON
// object terminated by RecordSeparator. Mirrors what
// `Clients.All.SendAsync("receiveMessage", message)` produces.
func EncodeInvocation(msg SignalRMessage) ([]byte, error) {
	inv := InvocationMessage{
		Type:      MessageTypeInvocation,
		Target:    "receiveMessage",
		Arguments: []any{msg},
	}
	return MarshalFrame(inv)
}

// EncodePing builds the bytes for one Ping frame. The transport
// sends this on a timer when no other message has gone out within
// the keep-alive interval.
func EncodePing() ([]byte, error) {
	return MarshalFrame(PingMessage{Type: MessageTypePing})
}

// MarshalFrame JSON-encodes v + appends the RecordSeparator. The
// canonical SignalR JSON-protocol frame shape.
func MarshalFrame(v any) ([]byte, error) {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("signalr: marshal frame: %w", err)
	}
	out := make([]byte, 0, len(buf)+1)
	out = append(out, buf...)
	out = append(out, RecordSeparator)
	return out, nil
}

// WriteFrame marshals v + writes it as a single RS-terminated
// frame to w. Returns whichever error fires first
// (marshal/io.Writer).
func WriteFrame(w io.Writer, v any) error {
	frame, err := MarshalFrame(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("signalr: write frame: %w", err)
	}
	return nil
}

// ReadFrame consumes bytes from r up to (but not including) the
// next RecordSeparator. Returns the JSON-object bytes WITHOUT the
// separator. EOF before any RS returns io.ErrUnexpectedEOF.
//
// Implemented as a simple byte-loop because the SignalR JSON
// protocol forbids RS bytes inside JSON values (RS is 0x1E, a
// non-printable control byte that JSON encoders escape as
// ``). The loop reads one byte at a time which is fine — the
// transport already buffers (WebSocket framing or long-polling
// HTTP body).
func ReadFrame(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	var one [1]byte
	for {
		n, err := r.Read(one[:])
		if n > 0 {
			if one[0] == RecordSeparator {
				return buf.Bytes(), nil
			}
			buf.WriteByte(one[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if buf.Len() == 0 {
					return nil, io.EOF
				}
				return nil, io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("signalr: read frame: %w", err)
		}
	}
}

// SplitFrames is a helper for long-polling style payloads that
// concatenate multiple RS-terminated frames in a single body.
// Returns each JSON-object portion (without the trailing RS).
// Empty input returns nil; a trailing RS produces an empty final
// frame which the caller should discard.
func SplitFrames(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{RecordSeparator})
	out := parts[:0]
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out
}
