package xbmc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// notificationImage is the Sonarr logo URL surfaced on the Kodi
// notification overlay. Mirrors C# `XbmcJsonApiProxy.Notify` at `:38`.
const notificationImage = "https://raw.github.com/Sonarr/Sonarr/develop/Logo/64.png"

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrSendFailed wraps a transport-level failure. Mirrors C#
// `XbmcJsonException`.
var ErrSendFailed = errors.New("xbmc: send failed")

// ErrJSONRPC is the distinguished sentinel for Kodi-side JSON-RPC
// error responses. Mirrors C# `XbmcJsonException` raised from
// `XbmcJsonApiProxy.CheckForError` at `:107-115`.
type ErrJSONRPC struct {
	Code    int
	Message string
}

// Error formats the JSON-RPC error.
func (e *ErrJSONRPC) Error() string {
	return fmt.Sprintf("xbmc: jsonrpc error code=%d message=%s", e.Code, e.Message)
}

// rpcRequest mirrors the JSON-RPC 2.0 request shape Kodi expects.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcEnvelope is the result/error fan-out the response always uses.
type rpcEnvelope struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// activePlayer mirrors the `Player.GetActivePlayers` result shape.
type activePlayer struct {
	PlayerID int    `json:"playerid"`
	Type     string `json:"type"`
}

// baseURL composes the {scheme}://{host}:{port}{urlBase} URL.
// Mirrors C# `HttpRequestBuilder.BuildBaseUrl` at `:78` + the
// `XbmcSettings.Address` computed property at `:69`.
func baseURL(s *Settings) string {
	scheme := "http"
	if s.UseSsl {
		scheme = "https"
	}
	urlBase := s.UrlBase
	if urlBase != "" && !strings.HasPrefix(urlBase, "/") {
		urlBase = "/" + urlBase
	}
	return scheme + "://" + s.Host + ":" + strconv.Itoa(s.Port) + urlBase
}

// rpcCall executes a single JSON-RPC method and returns the raw
// result bytes. Mirrors C# `XbmcJsonApiProxy.ProcessRequest` at
// `:76-96`.
func rpcCall(ctx context.Context, doer httpDoer, settings *Settings, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("xbmc: marshal: %w (cause: %w)", ErrSendFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(settings), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("xbmc: build request: %w (cause: %w)", ErrSendFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(settings.Username) != "" {
		req.SetBasicAuth(settings.Username, settings.Password)
	}

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xbmc: do: %w (cause: %w)", ErrSendFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("xbmc: read: %w (cause: %w)", ErrSendFailed, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrSendFailed, resp.StatusCode, string(raw))
	}
	var env rpcEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("xbmc: unmarshal envelope: %w (cause: %w)", ErrSendFailed, err)
	}
	if env.Error != nil {
		return nil, &ErrJSONRPC{Code: env.Error.Code, Message: env.Error.Message}
	}
	return env.Result, nil
}

// notify calls `GUI.ShowNotification` with title/message/image/
// displaytime. Mirrors C# `:36-39`.
func notify(ctx context.Context, doer httpDoer, settings *Settings, title, message string) error {
	params := map[string]any{
		"title":       title,
		"message":     message,
		"image":       notificationImage,
		"displaytime": settings.DisplayTime * 1000,
	}
	_, err := rpcCall(ctx, doer, settings, "GUI.ShowNotification", params)
	return err
}

// updateLibrary calls `VideoLibrary.Scan` (with optional path scope).
// Mirrors C# `XbmcJsonApiProxy.UpdateLibrary` at `:41-55`.
func updateLibrary(ctx context.Context, doer httpDoer, settings *Settings, path string) error {
	var params any
	if path != "" {
		params = map[string]any{"directory": path}
	}
	_, err := rpcCall(ctx, doer, settings, "VideoLibrary.Scan", params)
	return err
}

// cleanLibrary calls `VideoLibrary.Clean`. Mirrors C# `:57-60`.
func cleanLibrary(ctx context.Context, doer httpDoer, settings *Settings) error {
	_, err := rpcCall(ctx, doer, settings, "VideoLibrary.Clean", nil)
	return err
}

// videoPlayerActive calls `Player.GetActivePlayers` and reports
// whether any player has type="video". Mirrors C#
// `CheckIfVideoPlayerOpen` at `XbmcService.cs:114-125`.
func videoPlayerActive(ctx context.Context, doer httpDoer, settings *Settings) (bool, error) {
	raw, err := rpcCall(ctx, doer, settings, "Player.GetActivePlayers", nil)
	if err != nil {
		return false, err
	}
	var players []activePlayer
	if err := json.Unmarshal(raw, &players); err != nil {
		return false, fmt.Errorf("xbmc: unmarshal players: %w (cause: %w)", ErrSendFailed, err)
	}
	for _, p := range players {
		if p.Type == "video" {
			return true, nil
		}
	}
	return false, nil
}
