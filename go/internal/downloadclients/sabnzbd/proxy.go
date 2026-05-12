package sabnzbd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HTTPDoer is the narrow http-client interface [Proxy] consumes. The
// production binding is `*http.Client` (or the project's
// `*httpclient.Client` which embeds it); tests pass a stub or a
// wrapper around an httptest.Server.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Proxy is the HTTP wrapper around the SABnzbd JSON API. Mirrors
// `SabnzbdProxy.cs:27-247` — but ships only the endpoints sub-arc
// 14a's [Sabnzbd] Provider consumes (DownloadNzb + GetVersion).
// GetQueue / GetHistory / GetConfig / GetFullStatus /
// RemoveFromQueue / RemoveFromHistory / RetryDownload are sub-arc
// 14b territory.
//
// Construction: [NewProxy] binds an [HTTPDoer] + logger. The Doer's
// concurrency-safety is the caller's responsibility; the Proxy
// itself is goroutine-safe (stateless beyond the wired deps).
type Proxy struct {
	doer HTTPDoer
	log  *slog.Logger
}

// NewProxy binds the deps. doer is REQUIRED; log defaults to
// slog.Default() when nil.
func NewProxy(doer HTTPDoer, log *slog.Logger) (*Proxy, error) {
	if doer == nil {
		return nil, errors.New("sabnzbd: NewProxy: nil HTTPDoer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{doer: doer, log: log}, nil
}

// BuildBaseURL returns the absolute URL for an SAB API call, with
// the protocol (http/https) + host + port + optional URL base
// composed per `SabnzbdProxy.GetBaseUrl` at `:38-44`.
//
// relativePath is appended after the URL base (e.g. "api" yields
// "http://localhost:8080/api" or
// "https://example.com/sabnzbd/api"). Empty relativePath returns
// the bare base URL.
func (p *Proxy) BuildBaseURL(settings Settings, relativePath string) string {
	scheme := "http"
	if settings.UseSsl {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s:%d", scheme, settings.Host, settings.Port)
	if ub := strings.TrimSpace(settings.UrlBase); ub != "" {
		if !strings.HasPrefix(ub, "/") {
			ub = "/" + ub
		}
		base += strings.TrimRight(ub, "/")
	}
	if relativePath != "" {
		base += "/" + strings.TrimLeft(relativePath, "/")
	}
	return base
}

// addResponse is the SAB `addfile` / `addurl` response. Mirrors C#
// `SabnzbdAddResponse` at `Responses/SabnzbdAddResponse.cs:1-10`:
// `{"status": bool, "nzo_ids": ["SABnzbd_nzo_xxx"]}`.
type addResponse struct {
	Status bool     `json:"status"`
	IDs    []string `json:"nzo_ids"`
}

// versionResponse is the SAB `version` response shape:
// `{"version": "3.7.2"}`.
type versionResponse struct {
	Version string `json:"version"`
}

// jsonError is the SAB error envelope. Mirrors C# `SabnzbdJsonError`
// at `SabnzbdJsonError.cs:1-13`. SAB returns `{"status":"false",
// "error":"<msg>"}` on validation failures (e.g. wrong API key).
type jsonError struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

// Failed reports whether SAB returned a failure envelope. Mirrors
// the C# `SabnzbdJsonError.Failed` getter at `SabnzbdJsonError.cs:10-11`
// which uses `Status.Equals("false", InvariantCultureIgnoreCase)`.
// EqualFold yields the case-insensitive match and naturally returns
// false for the empty/whitespace inputs the C# guard handled with
// `!IsNullOrWhiteSpace`.
func (e jsonError) Failed() bool { return strings.EqualFold(e.Status, "false") }

// DownloadNzb POSTs an NZB body to the SAB `addfile` endpoint and
// returns the assigned nzo_id. Mirrors C# `SabnzbdProxy.DownloadNzb`
// at `:46-62`.
//
// Returns a typed [ErrEmptyResponse] when SAB accepted the upload
// but returned no nzo_id (the C# code throws
// DownloadClientRejectedReleaseException — the Go port surfaces an
// error so the orchestrator can decide whether to retry or fail
// permanently).
func (p *Proxy) DownloadNzb(ctx context.Context, nzbData []byte, filename, category string, priority int, settings Settings) (string, error) {
	q := url.Values{}
	q.Set("cat", category)
	q.Set("priority", strconv.Itoa(priority))

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("name", filename)
	if err != nil {
		return "", fmt.Errorf("sabnzbd: DownloadNzb: form-create: %w", err)
	}
	if _, err := part.Write(nzbData); err != nil {
		return "", fmt.Errorf("sabnzbd: DownloadNzb: form-write: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("sabnzbd: DownloadNzb: form-close: %w", err)
	}

	req, err := p.buildRequest(ctx, http.MethodPost, "addfile", q, settings, body, writer.FormDataContentType())
	if err != nil {
		return "", err
	}
	respBody, err := p.execute(req)
	if err != nil {
		return "", err
	}

	if err := checkForError(respBody); err != nil {
		return "", err
	}
	var addResp addResponse
	if err := json.Unmarshal(respBody, &addResp); err != nil {
		// SAB sometimes returns just "ok" / "true" for the addfile
		// success path (older versions); the C# code at `:55-60`
		// tolerates the deserialisation failure as success. Mirror:
		// fall through to ErrEmptyResponse rather than propagating
		// the parse error.
		return "", ErrEmptyResponse
	}
	if len(addResp.IDs) == 0 {
		return "", ErrEmptyResponse
	}
	return addResp.IDs[0], nil
}

// GetQueue ports C# `SabnzbdProxy.GetQueue` at `SabnzbdProxy.cs:116-130`.
// Returns the SAB queue paginated by (start, limit). When start==0 +
// limit==0, SAB returns every queued item.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) GetQueue(ctx context.Context, start, limit int, settings Settings) (*SabnzbdQueue, error) {
	q := url.Values{}
	q.Set("start", strconv.Itoa(start))
	q.Set("limit", strconv.Itoa(limit))
	if cat := strings.TrimSpace(settings.TvCategory); cat != "" {
		q.Set("category", cat)
	}
	body, err := p.callGet(ctx, "queue", q, settings)
	if err != nil {
		return nil, err
	}
	var env queueResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("sabnzbd: GetQueue: unmarshal: %w", err)
	}
	return &env.Queue, nil
}

// GetHistory ports C# `SabnzbdProxy.GetHistory` at `:132-146`.
// SAB caps history requests at the per-instance DownloadClientHistoryLimit
// (default 30); the caller (Sabnzbd.GetItems) supplies the configured
// limit. start=0 + limit=0 yields the SAB-default page.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) GetHistory(ctx context.Context, start, limit int, settings Settings) (*SabnzbdHistory, error) {
	q := url.Values{}
	q.Set("start", strconv.Itoa(start))
	q.Set("limit", strconv.Itoa(limit))
	if cat := strings.TrimSpace(settings.TvCategory); cat != "" {
		q.Set("category", cat)
	}
	body, err := p.callGet(ctx, "history", q, settings)
	if err != nil {
		return nil, err
	}
	var env historyResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("sabnzbd: GetHistory: unmarshal: %w", err)
	}
	return &env.History, nil
}

// GetConfig ports C# `SabnzbdProxy.GetConfig` at `:97-104`. Returns
// the SAB global-config sub-tree (Misc + Categories + Sorters) used
// by [Sabnzbd.GetStatus] for the OutputRootFolders + SortingMode
// projection.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) GetConfig(ctx context.Context, settings Settings) (*SabnzbdConfig, error) {
	body, err := p.callGet(ctx, "get_config", nil, settings)
	if err != nil {
		return nil, err
	}
	var env configResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("sabnzbd: GetConfig: unmarshal: %w", err)
	}
	return &env.Config, nil
}

// GetFullStatus ports C# `SabnzbdProxy.GetFullStatus` at `:106-114`.
// Adds the `skip_dashboard=1` query param the C# code uses to avoid
// returning the heavy dashboard payload. Used by [Sabnzbd.GetStatus]
// to resolve the absolute complete_dir when the SAB-side value is
// relative (SAB >= 2.0).
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) GetFullStatus(ctx context.Context, settings Settings) (*SabnzbdFullStatus, error) {
	q := url.Values{}
	q.Set("skip_dashboard", "1")
	body, err := p.callGet(ctx, "fullstatus", q, settings)
	if err != nil {
		return nil, err
	}
	var env fullStatusResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("sabnzbd: GetFullStatus: unmarshal: %w", err)
	}
	return &env.Status, nil
}

// RemoveFromQueue ports C# `SabnzbdProxy.RemoveFromQueue` at `:64-72`.
// Tells SAB to drop the in-progress download. deleteData=true also
// deletes the partial files on disk.
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) RemoveFromQueue(ctx context.Context, id string, deleteData bool, settings Settings) error {
	q := url.Values{}
	q.Set("name", "delete")
	q.Set("del_files", boolToZeroOne(deleteData))
	q.Set("value", id)
	_, err := p.callGet(ctx, "queue", q, settings)
	return err
}

// RemoveFromHistory ports C# `SabnzbdProxy.RemoveFromHistory` at
// `:74-83`. Drops a completed item from SAB's history. The
// `archive` param is the C# inverse-of-deletePermanently mapping
// (deletePermanently=true → archive=0 → fully purged;
// deletePermanently=false → archive=1 → moved to history's archive).
//
// Slice 4.BS (C-cleanup sub-arc C2a).
func (p *Proxy) RemoveFromHistory(ctx context.Context, id string, deleteData, deletePermanently bool, settings Settings) error {
	q := url.Values{}
	q.Set("name", "delete")
	q.Set("del_files", boolToZeroOne(deleteData))
	q.Set("value", id)
	if deletePermanently {
		q.Set("archive", "0")
	} else {
		q.Set("archive", "1")
	}
	_, err := p.callGet(ctx, "history", q, settings)
	return err
}

// callGet is the shared GET-then-checkForError helper for the
// JSON-API endpoints. Mirrors the C# `ProcessRequest` happy path at
// `SabnzbdProxy.cs:187-220` minus the request-builder ceremony.
func (p *Proxy) callGet(ctx context.Context, mode string, extraQuery url.Values, settings Settings) ([]byte, error) {
	if extraQuery == nil {
		extraQuery = url.Values{}
	}
	req, err := p.buildRequest(ctx, http.MethodGet, mode, extraQuery, settings, nil, "")
	if err != nil {
		return nil, err
	}
	body, err := p.execute(req)
	if err != nil {
		return nil, err
	}
	if err := checkForError(body); err != nil {
		return nil, err
	}
	return body, nil
}

func boolToZeroOne(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// GetVersion returns the SAB version string (e.g. "3.7.2"). Mirrors
// C# `SabnzbdProxy.GetVersion` at `:85-95`. Used by [Sabnzbd.Test]
// for the version-check FluentValidation rule.
func (p *Proxy) GetVersion(ctx context.Context, settings Settings) (string, error) {
	req, err := p.buildRequest(ctx, http.MethodGet, "version", url.Values{}, settings, nil, "")
	if err != nil {
		return "", err
	}
	body, err := p.execute(req)
	if err != nil {
		return "", err
	}
	if err := checkForError(body); err != nil {
		return "", err
	}
	var resp versionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// Older SAB versions return the version string as plain
		// text. Tolerate.
		return strings.TrimSpace(string(body)), nil
	}
	return resp.Version, nil
}

// buildRequest composes an authenticated request against the SAB
// JSON API. Mirrors C# `SabnzbdProxy.BuildRequest` at `:162-185`:
//
//   - `mode` query param identifies the API endpoint.
//   - `apikey` query param when Settings.APIKey is set, else
//     `ma_username` + `ma_password` for legacy auth.
//   - `output=json` always appended.
//
// body / contentType are optional for POST endpoints. For GET, pass
// nil + "".
func (p *Proxy) buildRequest(
	ctx context.Context,
	method, mode string,
	extraQuery url.Values,
	settings Settings,
	body io.Reader,
	contentType string,
) (*http.Request, error) {
	endpoint := p.BuildBaseURL(settings, "api")
	q := url.Values{}
	q.Set("mode", mode)
	for k, vs := range extraQuery {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if strings.TrimSpace(settings.APIKey) != "" {
		q.Set("apikey", settings.APIKey)
	} else {
		q.Set("ma_username", settings.Username)
		q.Set("ma_password", settings.Password)
	}
	q.Set("output", "json")
	endpoint += "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: build request %q: %w", mode, err)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

// execute fires the request through the [HTTPDoer] and returns the
// raw body. Wraps non-2xx and transport errors with the typed
// [ErrUnavailable] / [ErrUnexpected] sentinels.
func (p *Proxy) execute(req *http.Request) ([]byte, error) {
	resp, err := p.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("sabnzbd: read body: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status=%d body=%s", ErrUnexpected, resp.StatusCode, truncate(body, 256))
	}
	return body, nil
}

// checkForError parses the JSON error envelope. Mirrors
// `SabnzbdProxy.CheckForError` at `:222-246`. Returns nil on
// success bodies (status=true or non-JSON OK responses).
func checkForError(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	// Plain-text "error: …" fallback path (C# `:228-233`).
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(bytes.ToLower(trimmed), []byte("error")) {
		msg := strings.TrimPrefix(string(trimmed), "error: ")
		return fmt.Errorf("%w: %s", ErrSabError, msg)
	}
	var env jsonError
	if err := json.Unmarshal(body, &env); err != nil {
		// Not a JSON error envelope → treat as success (the caller
		// will deserialise the typed response). Matches C# `:223`
		// `TryDeserialize` which falls through on parse failure.
		return nil
	}
	if env.Failed() {
		return fmt.Errorf("%w: %s", ErrSabError, env.Error)
	}
	return nil
}

// truncate clamps a byte slice for log + error-message use. Avoids
// dumping multi-KB SAB error pages into log lines.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// Typed sentinels for the Proxy's failure modes. Callers
// (Sabnzbd.Test / Sabnzbd.Download) use errors.Is to classify.
var (
	// ErrEmptyResponse — SAB accepted the request but returned no
	// nzo_id. Mirrors C# `DownloadClientRejectedReleaseException`
	// at `Sabnzbd.cs:49`.
	ErrEmptyResponse = errors.New("sabnzbd: empty response (no nzo_id assigned)")

	// ErrUnavailable — the SAB host couldn't be reached (transport
	// error, DNS failure, TLS handshake failure). Mirrors C#
	// `DownloadClientUnavailableException` at `:205-214`.
	ErrUnavailable = errors.New("sabnzbd: unavailable")

	// ErrUnexpected — SAB returned a non-2xx HTTP status that
	// wasn't a JSON error envelope.
	ErrUnexpected = errors.New("sabnzbd: unexpected response")

	// ErrSabError — SAB returned a JSON error envelope or a
	// plain-text "error: …" body. Mirrors C#
	// `DownloadClientException` at `:244`.
	ErrSabError = errors.New("sabnzbd: error response")
)
