package hadouken

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
)

// base64StdEncoding aliases the stdlib codec; defined as a package-
// level var so the [base64Encode] helper doesn't pull the import
// list onto every call site.
var base64StdEncoding = base64.StdEncoding

// HTTPDoer is the [*http.Client] slice the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// SystemInfo ports C# `HadoukenSystemInfo` at
// `Models/HadoukenSystemInfo.cs`. The Test path reads
// `Versions["hadouken"]` to gate against [minVersion].
type SystemInfo struct {
	Commitish string            `json:"commitish"`
	Branch    string            `json:"branch"`
	Versions  map[string]string `json:"versions"`
}

// Proxy is the JSON-RPC 2.0 client for a Hadouken endpoint. Mirrors
// C# `HadoukenProxy` at `HadoukenProxy.cs:22-184`. Goroutine-safe:
// stateless beyond the atomic request-id counter.
type Proxy struct {
	http  HTTPDoer
	log   *slog.Logger
	reqID atomic.Int64
}

// NewProxy constructs a Proxy. http=nil binds [http.DefaultClient];
// log=nil binds [slog.Default].
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// endpointURL composes the JSON-RPC POST URL. Mirrors C#
// `HadoukenProxy.ProcessRequest` URL composition at `:72-73`:
//
//	BuildBaseUrl(UseSsl, Host, Port, UrlBase) + "/api"
func endpointURL(s Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, s.Host, s.Port)
	ub := strings.TrimSpace(s.UrlBase)
	if ub != "" && ub != "/" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	return base + "/api"
}

// rpcRequest is the canonical JSON-RPC 2.0 envelope. Mirrors C#
// `JsonRpcRequestBuilder` output at `HadoukenProxy.cs:75`.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int64  `json:"id"`
}

// rpcResponse is the canonical JSON-RPC 2.0 reply envelope. Result
// is `json.RawMessage` so per-method callers can deserialise into
// the typed shape they need.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

// rpcError is the JSON-RPC 2.0 error object the server returns
// on a method failure. Mirrors C# `Json.Deserialize<JsonRpcResponse<T>>`
// + the `result.Error` check at `:103-106`.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) String() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("code=%d msg=%s", e.Code, e.Message)
}

// call submits one JSON-RPC method invocation against the
// Hadouken endpoint and unmarshals the `result` field into out.
// out=nil discards the result body.
//
// Error mapping mirrors C# `ProcessRequest<T>` at
// `HadoukenProxy.cs:70-108`:
//
//   - Transport failure → wrapped [ErrUnavailable]; the Test path
//     surfaces this as the localized "unable to connect" failure.
//   - HTTP 401 → [ErrAuth]; Test surfaces as the password failure.
//   - JSON-RPC `error` non-nil → [ErrRPC] carrying the server-side
//     code+message.
func (p *Proxy) call(ctx context.Context, s Settings, method string, params []any, out any) error {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      p.reqID.Add(1),
	})
	if err != nil {
		return fmt.Errorf("hadouken: marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(s), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("hadouken: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip,deflate")
	if u := strings.TrimSpace(s.Username); u != "" {
		req.SetBasicAuth(u, s.Password)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: %s", ErrAuth, method)
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("hadouken: %s read body: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %s status %d: %s", ErrUnavailable, method, resp.StatusCode,
			strings.TrimSpace(truncate(string(respBody), 256)))
	}

	var env rpcResponse
	if err := json.Unmarshal(respBody, &env); err != nil {
		return fmt.Errorf("hadouken: %s unmarshal envelope: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%w: %s: %s", ErrRPC, method, env.Error.String())
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("hadouken: %s unmarshal result: %w", method, err)
		}
	}
	return nil
}

// GetSystemInfo fires `core.getSystemInfo`. Used by the Test path
// to read the running Hadouken version and enforce [minVersion].
func (p *Proxy) GetSystemInfo(ctx context.Context, s Settings) (*SystemInfo, error) {
	var info SystemInfo
	if err := p.call(ctx, s, "core.getSystemInfo", []any{}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// AddTorrentFile fires `webui.addTorrent` with the base64-encoded
// .torrent body. Mirrors C# `HadoukenProxy.AddTorrentFile` at
// `HadoukenProxy.cs:50-53`. Returns whatever string the Hadouken
// server replies with (typically the infohash); the caller is
// responsible for uppercasing and the fallback-to-req.InfoHash
// branch when the response is empty.
//
// Params order matches the C# call:
//
//	["file", "<base64-encoded-torrent>", {"label": "<Category>"}]
func (p *Proxy) AddTorrentFile(ctx context.Context, s Settings, contents []byte) (string, error) {
	var result string
	params := []any{
		"file",
		base64Encode(contents),
		labelParam(s.Category),
	}
	if err := p.call(ctx, s, "webui.addTorrent", params, &result); err != nil {
		return "", err
	}
	return result, nil
}

// TorrentState ports C# `HadoukenTorrentState` at
// `Models/HadoukenTorrentState.cs`. The Hadouken server sends the
// state as an integer bitmask; [parseState] derives the discrete
// value below. Numeric values match the C# enum for wire-stable
// debugging — though the Go port never serialises them.
type TorrentState int

// TorrentState values mirror the C# enum order.
const (
	TorrentStateUnknown             TorrentState = 0
	TorrentStateQueuedForChecking   TorrentState = 1
	TorrentStateCheckingFiles       TorrentState = 2
	TorrentStateDownloadingMetadata TorrentState = 3
	TorrentStateDownloading         TorrentState = 4
	TorrentStateFinished            TorrentState = 5
	TorrentStateSeeding             TorrentState = 6
	TorrentStateAllocating          TorrentState = 7
	TorrentStateCheckingResumeData  TorrentState = 8
	TorrentStatePaused              TorrentState = 9
)

// Torrent is one entry from the `webui.list` tuple-row payload.
// Mirrors C# `HadoukenTorrent` at `Models/HadoukenTorrent.cs:3-19`.
// IsFinished is computed by [mapTorrent] post-parse (Progress >=
// 1000 — Hadouken reports per-mille).
type Torrent struct {
	InfoHash        string
	Progress        float64
	Name            string
	Label           string
	SavePath        string
	State           TorrentState
	IsFinished      bool
	TotalSize       int64
	DownloadedBytes int64
	UploadedBytes   int64
	DownloadRate    int64
	Error           string
}

// GetTorrents fires `webui.list` and returns the parsed Torrent
// slice. Mirrors C# `HadoukenProxy.GetTorrents` at
// `HadoukenProxy.cs:38-43` + the tuple-row mapper at `:111-160`.
// Per-row mapping errors are logged and the row is skipped — same
// behaviour as the C# `try/catch` at `:154-156`.
func (p *Proxy) GetTorrents(ctx context.Context, s Settings) ([]*Torrent, error) {
	var resp struct {
		Torrents [][]any `json:"torrents"`
	}
	if err := p.call(ctx, s, "webui.list", []any{}, &resp); err != nil {
		return nil, err
	}
	out := make([]*Torrent, 0, len(resp.Torrents))
	for i, row := range resp.Torrents {
		t, err := mapTorrent(row)
		if err != nil {
			p.log.Debug("hadouken: failed to map torrent row", "index", i, "err", err)
			continue
		}
		t.IsFinished = t.Progress >= 1000
		out = append(out, t)
	}
	return out, nil
}

// mapTorrent projects one webui.list tuple row into a [Torrent].
// Mirrors C# `MapTorrent` at `HadoukenProxy.cs:133-160`. Column
// indices match the C# bracket reads verbatim:
//
//	[0]=InfoHash [1]=StateBitmask [2]=Name [3]=TotalSize [4]=Progress
//	[5]=DownloadedBytes [6]=UploadedBytes [9]=DownloadRate
//	[11]=Label [21]=Error [26]=SavePath
//
// Indices unused by Sonarr (7, 8, 10, 12-20, 22-25, 27+) are
// skipped — matches C# `MapTorrent` which only reads the columns
// above. A row shorter than the highest required index (26) is a
// hard failure (returns error → caller skips the row).
func mapTorrent(row []any) (*Torrent, error) {
	if len(row) < 27 {
		return nil, fmt.Errorf("row has %d columns, need at least 27", len(row))
	}
	stateBitmask, err := readInt(row[1])
	if err != nil {
		return nil, fmt.Errorf("state column: %w", err)
	}
	totalSize, err := readInt64(row[3])
	if err != nil {
		return nil, fmt.Errorf("totalSize column: %w", err)
	}
	progress, err := readFloat64(row[4])
	if err != nil {
		return nil, fmt.Errorf("progress column: %w", err)
	}
	downloaded, err := readInt64(row[5])
	if err != nil {
		return nil, fmt.Errorf("downloadedBytes column: %w", err)
	}
	uploaded, err := readInt64(row[6])
	if err != nil {
		return nil, fmt.Errorf("uploadedBytes column: %w", err)
	}
	downRate, err := readInt64(row[9])
	if err != nil {
		return nil, fmt.Errorf("downloadRate column: %w", err)
	}
	return &Torrent{
		InfoHash:        readString(row[0]),
		State:           parseState(stateBitmask),
		Name:            readString(row[2]),
		TotalSize:       totalSize,
		Progress:        progress,
		DownloadedBytes: downloaded,
		UploadedBytes:   uploaded,
		DownloadRate:    downRate,
		Label:           readString(row[11]),
		Error:           readString(row[21]),
		SavePath:        readString(row[26]),
	}, nil
}

// parseState ports the C# bitmask→enum table at
// `HadoukenProxy.cs:162-182`. The bits are tested in priority
// order: Downloading > CheckingFiles > Paused > QueuedForChecking
// > Unknown — matching the C# if/else-if chain.
func parseState(state int) TorrentState {
	switch {
	case state&1 == 1:
		return TorrentStateDownloading
	case state&2 == 2:
		return TorrentStateCheckingFiles
	case state&32 == 32:
		return TorrentStatePaused
	case state&64 == 64:
		return TorrentStateQueuedForChecking
	default:
		return TorrentStateUnknown
	}
}

// readString coerces a JSON value into a string. Maps nil → ""
// (matches C# `Convert.ToString(null) == string.Empty`).
func readString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// readInt coerces a JSON-numeric value into int. Empty / non-
// numeric → 0 (matches C# `Convert.ToInt32(null/string) == 0` for
// the wire shapes Hadouken actually emits — integers come through
// as JSON numbers, never strings).
func readInt(v any) (int, error) {
	f, err := readFloat64(v)
	if err != nil {
		return 0, err
	}
	return int(f), nil
}

// readInt64 coerces a JSON-numeric value into int64.
func readInt64(v any) (int64, error) {
	f, err := readFloat64(v)
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}

// readFloat64 coerces a JSON-numeric value into float64. nil → 0;
// anything else that isn't a float64 (Go json.Unmarshal's only
// numeric target for `any`) is an error.
func readFloat64(v any) (float64, error) {
	if v == nil {
		return 0, nil
	}
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

// AddTorrentUri fires `webui.addTorrent` with a URL (magnet link
// or HTTP(S) DownloadURL). Mirrors C# `HadoukenProxy.AddTorrentUri`
// at `HadoukenProxy.cs:55-58`. C# discards the result string and
// returns void; the Go port follows suit — the caller composes the
// DownloadID from req.InfoHash since Hadouken does not echo the
// hash back on the URL-add path reliably.
//
// Params order matches the C# call:
//
//	["url", "<url-or-magnet>", {"label": "<Category>"}]
func (p *Proxy) AddTorrentUri(ctx context.Context, s Settings, url string) error {
	params := []any{
		"url",
		url,
		labelParam(s.Category),
	}
	return p.call(ctx, s, "webui.addTorrent", params, nil)
}

// labelParam builds the third positional arg to `webui.addTorrent`:
// `{"label": "<category>"}`. The C# code passes an anonymous
// object; the Go port emits the same JSON shape.
func labelParam(category string) map[string]string {
	return map[string]string{"label": category}
}

// base64Encode wraps [encoding/base64] without taking the import at
// the top of this file (it's not referenced anywhere else). Mirrors
// C# `Convert.ToBase64String` at `HadoukenProxy.cs:52`.
func base64Encode(contents []byte) string {
	return base64StdEncoding.EncodeToString(contents)
}

// truncate clips s to n chars with an ellipsis when overflowing.
// Used to bound error-message bodies inserted into wrap chains.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ErrUnavailable wraps every transport-level failure (network
// errors, 5xx responses, non-2xx that isn't 401). Mirrors C#
// `DownloadClientUnavailableException` at `:95,98`.
var ErrUnavailable = errors.New("hadouken: unavailable")

// ErrAuth signals HTTP 401 from the JSON-RPC endpoint — Username
// or Password is wrong. Mirrors C#
// `DownloadClientAuthenticationException` (raised from C# 401
// handling deep inside `_httpClient.Execute`).
var ErrAuth = errors.New("hadouken: authentication failed")

// ErrRPC wraps every Hadouken-side error object. Mirrors C#
// `DownloadClientException("Error response received from Hadouken")`
// at `:105`.
var ErrRPC = errors.New("hadouken: rpc error")
