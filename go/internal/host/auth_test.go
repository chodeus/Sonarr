package host

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type erroringResolver struct{ err error }

func (e erroringResolver) APIKey(context.Context) (string, error) { return "", e.err }

func TestParseAPIKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		query   string
		headers map[string]string
		want    string
	}{
		{"empty", "", nil, ""},
		{"query_wins", "apikey=q-key", map[string]string{"X-Api-Key": "h-key"}, "q-key"},
		{"header", "", map[string]string{"X-Api-Key": "h-key"}, "h-key"},
		{"bearer", "", map[string]string{"Authorization": "Bearer abc123"}, "abc123"},
		{"bearer_no_space", "", map[string]string{"Authorization": "BearerAbc"}, "BearerAbc"},
		{"bearer_global_replace", "", map[string]string{"Authorization": "Bearer foo Bearer bar"}, "foo bar"},
		{"empty_query_falls_through", "apikey=", map[string]string{"X-Api-Key": "h-key"}, "h-key"},
		// SignalR JS client passes the API key via `?access_token`
		// because WebSocket upgrades can't carry custom headers.
		{"signalr_access_token", "access_token=sr-key", nil, "sr-key"},
		// `?apikey` wins over `?access_token` when both supplied.
		{"apikey_beats_access_token", "apikey=q-key&access_token=sr-key", nil, "q-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := "/api"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if got := parseAPIKey(req); got != tc.want {
				t.Errorf("parseAPIKey: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRequireAPIKey(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mkServer := func(resolver APIKeyResolver) *Server {
		return New(Config{}, logger, probeFn(func(context.Context) error { return nil }), resolver)
	}

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	cases := []struct {
		name       string
		resolver   APIKeyResolver
		headers    map[string]string
		query      string
		wantStatus int
		wantBody   string
	}{
		{"ok_header", staticAPIKey("secret"), map[string]string{"X-Api-Key": "secret"}, "", 200, "ok"},
		{"ok_query", staticAPIKey("secret"), nil, "apikey=secret", 200, "ok"},
		{"ok_bearer", staticAPIKey("secret"), map[string]string{"Authorization": "Bearer secret"}, "", 200, "ok"},
		{"missing_key", staticAPIKey("secret"), nil, "", 401, ""},
		{"wrong_key", staticAPIKey("secret"), map[string]string{"X-Api-Key": "nope"}, "", 401, ""},
		{"stored_empty_rejects", staticAPIKey(""), map[string]string{"X-Api-Key": ""}, "", 401, ""},
		{"resolver_error", erroringResolver{err: errors.New("boom")}, map[string]string{"X-Api-Key": "anything"}, "", 401, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := mkServer(tc.resolver)
			url := "/api"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			srv.requireAPIKey(authed).ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("status: got %d want %d", rr.Code, tc.wantStatus)
			}
			if got := rr.Body.String(); got != tc.wantBody {
				t.Errorf("body: got %q want %q", got, tc.wantBody)
			}
		})
	}
}
