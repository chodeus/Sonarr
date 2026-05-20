package aria2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// HTTPDoer is the slice of [*http.Client] the [Proxy] depends on.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Proxy is the XML-RPC client for an aria2c endpoint. Mirrors C#
// `Aria2Proxy` at `Aria2Proxy.cs:21-180`. Goroutine-safe: stateless
// beyond construction-time deps.
type Proxy struct {
	http HTTPDoer
	log  *slog.Logger
}

// NewProxy constructs a Proxy. http defaults to http.DefaultClient
// when nil; log defaults to slog.Default.
func NewProxy(httpDoer HTTPDoer, log *slog.Logger) *Proxy {
	if httpDoer == nil {
		httpDoer = http.DefaultClient
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{http: httpDoer, log: log}
}

// endpointURL builds the absolute URL the XML-RPC POST targets.
// Mirrors C# `XmlRpcRequestBuilder` constructor at `Aria2Proxy.cs:155`.
func endpointURL(settings Settings) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	rpcPath := strings.TrimSpace(settings.RpcPath)
	if rpcPath == "" {
		rpcPath = "/rpc"
	}
	if !strings.HasPrefix(rpcPath, "/") {
		rpcPath = "/" + rpcPath
	}
	return fmt.Sprintf("%s://%s:%d%s", scheme, settings.Host, settings.Port, rpcPath)
}

// tokenArg returns the `token:<SecretToken>` string aria2 expects
// as the first arg on every authenticated method call. Mirrors C#
// `GetToken` at `Aria2Proxy.cs:148-151`.
func tokenArg(settings Settings) string {
	return "token:" + settings.SecretToken
}

// call submits an XML-RPC methodCall and returns the parsed
// response's first param value. FaultError is surfaced verbatim;
// HTTP-level failures wrap the underlying error.
func (p *Proxy) call(ctx context.Context, settings Settings, method string, args ...any) (*rawValue, error) {
	body, err := encodeCall(method, args...)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("aria2: build request: %w", err)
	}
	req.Header.Set("Content-Type", "text/xml")
	req.Header.Set("Accept", "text/xml")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aria2: %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("aria2: %s read body: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aria2: %s status %d: %s", method, resp.StatusCode, strings.TrimSpace(truncate(string(respBody), 256)))
	}
	value, err := decodeResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("aria2: %s: %w", method, err)
	}
	return value, nil
}

// GetVersion fires aria2.getVersion + reads the `version` struct
// field from the response. Mirrors C# `GetVersion` at `:33-41`.
// Returns the version string (e.g. "1.36.0").
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	value, err := p.call(ctx, settings, "aria2.getVersion", tokenArg(settings))
	if err != nil {
		return "", err
	}
	if v, ok := value.structField("version"); ok {
		return v.stringValue(), nil
	}
	return "", errors.New("aria2: getVersion: 'version' field missing from response")
}

// AddMagnet fires aria2.addUri with the magnet/URL wrapped in a
// single-element URI list + the options dict (dir → Settings.Directory
// when set). Returns the GID aria2 assigned to the new download.
// Mirrors C# `AddMagnet` at `Aria2Proxy.cs:99-112`.
func (p *Proxy) AddMagnet(ctx context.Context, magnetOrURL string, settings Settings) (string, error) {
	opts := buildAddOptions(settings)
	value, err := p.call(ctx, settings, "aria2.addUri",
		tokenArg(settings),
		[]string{magnetOrURL},
		opts,
	)
	if err != nil {
		return "", err
	}
	gid := value.stringValue()
	if gid == "" {
		return "", errors.New("aria2: addUri: empty GID in response")
	}
	return gid, nil
}

// AddTorrent fires aria2.addTorrent with the .torrent file bytes
// (base64-encoded by the encoder) + an empty URI list + the options
// dict. Returns the GID. Mirrors C# `AddTorrent` at `:114-129`.
//
// The empty-URI-list second arg is REQUIRED — aria2c rejects
// addTorrent calls that have options but no URI-list slot. The C#
// code's `emptyListOfUris` comment at `:117` documents this.
func (p *Proxy) AddTorrent(ctx context.Context, torrentBody []byte, settings Settings) (string, error) {
	opts := buildAddOptions(settings)
	value, err := p.call(ctx, settings, "aria2.addTorrent",
		tokenArg(settings),
		torrentBody,
		[]string{}, // empty URI list — required when options are passed.
		opts,
	)
	if err != nil {
		return "", err
	}
	gid := value.stringValue()
	if gid == "" {
		return "", errors.New("aria2: addTorrent: empty GID in response")
	}
	return gid, nil
}

// buildAddOptions returns the options dict for AddMagnet /
// AddTorrent. Empty when no overrides apply — passes through to
// aria2's defaults. Currently only `dir` is overrideable; future
// settings (max-connections, split-count, etc.) can extend this
// without touching the call sites.
func buildAddOptions(settings Settings) map[string]string {
	opts := map[string]string{}
	if strings.TrimSpace(settings.Directory) != "" {
		opts["dir"] = settings.Directory
	}
	return opts
}

// AriaStatus is the per-torrent shape returned by aria2.tellActive /
// tellWaiting / tellStopped. Mirrors C# `Aria2Status` at
// `Aria2Containers.cs:97-160`. Numeric fields are strings on the
// wire — preserved as strings here; the Provider parses to int64
// at projection time.
type AriaStatus struct {
	GID             string
	InfoHash        string
	Status          string // "active" / "waiting" / "paused" / "complete" / "error" / "removed"
	TotalLength     string
	CompletedLength string
	UploadLength    string
	DownloadSpeed   string
	ErrorMessage    string
	BittorrentName  string   // from bittorrent.info.name
	FilePaths       []string // file.path entries
}

// GetTorrents fires the 3-call fanout — tellActive + tellWaiting +
// tellStopped — and returns the union. Mirrors C#
// `Aria2Proxy.GetTorrents` at `:74-87`. tellWaiting + tellStopped
// take (offset=0, num=10240) per the C# `0, 10 * 1024` args.
func (p *Proxy) GetTorrents(ctx context.Context, settings Settings) ([]AriaStatus, error) {
	active, err := p.tellMethod(ctx, settings, "aria2.tellActive")
	if err != nil {
		return nil, err
	}
	waiting, err := p.tellMethod(ctx, settings, "aria2.tellWaiting", 0, 10*1024)
	if err != nil {
		return nil, err
	}
	stopped, err := p.tellMethod(ctx, settings, "aria2.tellStopped", 0, 10*1024)
	if err != nil {
		return nil, err
	}
	out := make([]AriaStatus, 0, len(active)+len(waiting)+len(stopped))
	out = append(out, active...)
	out = append(out, waiting...)
	out = append(out, stopped...)
	return out, nil
}

func (p *Proxy) tellMethod(ctx context.Context, settings Settings, method string, extraArgs ...any) ([]AriaStatus, error) {
	args := append([]any{tokenArg(settings)}, extraArgs...)
	value, err := p.call(ctx, settings, method, args...)
	if err != nil {
		return nil, err
	}
	rows := value.arrayElements()
	out := make([]AriaStatus, 0, len(rows))
	for _, row := range rows {
		out = append(out, decodeAriaStatus(row))
	}
	return out, nil
}

// decodeAriaStatus projects one struct-typed rawValue into
// [AriaStatus]. Missing fields → zero values.
func decodeAriaStatus(v rawValue) AriaStatus {
	var s AriaStatus
	if v.Struct == nil {
		return s
	}
	for _, m := range v.Struct.Member {
		switch m.Name {
		case "gid":
			s.GID = m.Value.stringValue()
		case "infoHash":
			s.InfoHash = m.Value.stringValue()
		case "status":
			s.Status = m.Value.stringValue()
		case "totalLength":
			s.TotalLength = m.Value.stringValue()
		case "completedLength":
			s.CompletedLength = m.Value.stringValue()
		case "uploadLength":
			s.UploadLength = m.Value.stringValue()
		case "downloadSpeed":
			s.DownloadSpeed = m.Value.stringValue()
		case "errorMessage":
			s.ErrorMessage = m.Value.stringValue()
		case "bittorrent":
			// bittorrent.info.name → BittorrentName. The C# tree at
			// Aria2Containers.cs:79-92 uses Descendants("member") to
			// walk the nested chain; we do an explicit field-by-field
			// drill to keep the decoder typed.
			if info, ok := m.Value.structField("info"); ok {
				if name, ok := info.structField("name"); ok {
					s.BittorrentName = name.stringValue()
				}
			}
		case "files":
			for _, fileRow := range m.Value.arrayElements() {
				if path, ok := fileRow.structField("path"); ok {
					s.FilePaths = append(s.FilePaths, path.stringValue())
				}
			}
		}
	}
	return s
}

// RemoveTorrent fires aria2.forceRemove for active downloads.
// Mirrors C# `RemoveTorrent` at `:131-138`.
func (p *Proxy) RemoveTorrent(ctx context.Context, gid string, settings Settings) error {
	value, err := p.call(ctx, settings, "aria2.forceRemove", tokenArg(settings), gid)
	if err != nil {
		return err
	}
	if returned := value.stringValue(); returned != gid {
		return fmt.Errorf("aria2: forceRemove returned %q for gid %q", returned, gid)
	}
	return nil
}

// RemoveCompletedTorrent fires aria2.removeDownloadResult for
// completed / errored / removed downloads. The response is "OK" on
// success. Mirrors C# `RemoveCompletedTorrent` at `:140-147`.
func (p *Proxy) RemoveCompletedTorrent(ctx context.Context, gid string, settings Settings) error {
	value, err := p.call(ctx, settings, "aria2.removeDownloadResult", tokenArg(settings), gid)
	if err != nil {
		return err
	}
	if v := value.stringValue(); v != "OK" {
		return fmt.Errorf("aria2: removeDownloadResult returned %q for gid %q (want OK)", v, gid)
	}
	return nil
}

// GetGlobals fires aria2.getGlobalOption + decodes the struct
// response into a map. Mirrors C# `GetGlobals` at `:89-96`. Used
// by GetStatus to populate OutputRootFolders from the global
// `dir` option.
func (p *Proxy) GetGlobals(ctx context.Context, settings Settings) (map[string]string, error) {
	value, err := p.call(ctx, settings, "aria2.getGlobalOption", tokenArg(settings))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if value.Struct == nil {
		return out, nil
	}
	for _, m := range value.Struct.Member {
		out[m.Name] = m.Value.stringValue()
	}
	return out, nil
}

// ErrAuthFailed is a sentinel placeholder — aria2's auth-failure
// surfaces as a FaultError with a specific message (e.g.
// "Unauthorized"); callers should match against the FaultError's
// Message field. Exposed for symmetry with the rTorrent / utorrent
// proxies.
var ErrAuthFailed = errors.New("aria2: authentication failed")

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
