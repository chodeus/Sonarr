package xbmc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/xbmc"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Method string
	Body   []byte
}

// rpcServer returns an httptest server that dispatches per JSON-RPC
// method via the methodResponses map. Calls are recorded into the
// capture slice. Unmatched methods return a generic empty result.
func rpcServer(t *testing.T, methodResponses map[string]string, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		*capture = append(*capture, captured{Method: req.Method, Body: body})
		if resp, ok := methodResponses[req.Method]; ok {
			_, _ = w.Write([]byte(resp))
			return
		}
		_, _ = w.Write([]byte(`{"result":"OK","id":1,"jsonrpc":"2.0"}`))
	}))
}

func settingsJSON(t *testing.T, s xbmc.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeXbmc(t *testing.T, srv *httptest.Server, extras func(*xbmc.Settings)) notifications.Provider {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s := xbmc.Settings{
		Host:        u.Hostname(),
		Port:        port,
		UrlBase:     "/",
		DisplayTime: 5,
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: xbmc.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := xbmc.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func methodsOf(c []captured) []string {
	out := make([]string, len(c))
	for i, x := range c {
		out[i] = x.Method
	}
	return out
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    xbmc.Settings
		want string
	}{
		{"NoHost", xbmc.Settings{DisplayTime: 5}, "Host"},
		{"DisplayTimeTooLow", xbmc.Settings{Host: "h", DisplayTime: 1}, "DisplayTime"},
		{"OK", xbmc.Settings{Host: "h", DisplayTime: 5}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fails := c.s.Validate()
			if c.want == "" {
				if len(fails) != 0 {
					t.Errorf("expected no failures, got %+v", fails)
				}
				return
			}
			found := false
			for _, f := range fails {
				if f.PropertyName == c.want {
					found = true
				}
			}
			if !found {
				t.Errorf("missing %s failure; got %+v", c.want, fails)
			}
		})
	}
}

func TestXbmc_OnGrab_NotifyOnlyWhenEnabled(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) { s.Notify = true })
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 || capture[0].Method != "GUI.ShowNotification" {
		t.Errorf("methods=%v", methodsOf(capture))
	}
	if !strings.Contains(string(capture[0].Body), `"title":"Sonarr - Grabbed"`) {
		t.Errorf("missing title in body: %s", string(capture[0].Body))
	}
	if !strings.Contains(string(capture[0].Body), `"displaytime":5000`) {
		t.Errorf("displaytime should be 5000ms, got %s", string(capture[0].Body))
	}
}

func TestXbmc_NotifyDisabled_OnGrabIsSilent(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, nil)
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if len(capture) != 0 {
		t.Errorf("expected zero calls when Notify is off, got %v", methodsOf(capture))
	}
}

func TestXbmc_OnImportComplete_NotifyImmediateLibraryWorkDeferred(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, map[string]string{
		"Player.GetActivePlayers": `{"result":[],"id":1,"jsonrpc":"2.0"}`,
	}, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) {
		s.Notify = true
		s.UpdateLibrary = true
		s.CleanLibrary = true
	})
	msg := notifications.ImportCompleteMessage{Series: &series.Series{Path: "/srv/Show"}, Message: "ok"}
	if err := prov.OnImportComplete(t.Context(), msg); err != nil {
		t.Fatalf("OnImportComplete: %v", err)
	}
	// Notify fires immediately; library work is queued for the next
	// ProcessQueue tick (mirrors C# MediaServerUpdateQueue).
	if got := methodsOf(capture); !equal(got, []string{"GUI.ShowNotification"}) {
		t.Fatalf("after OnImportComplete: methods=%v want GUI.ShowNotification only", got)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	want := []string{"GUI.ShowNotification", "Player.GetActivePlayers", "VideoLibrary.Scan", "VideoLibrary.Clean"}
	if got := methodsOf(capture); !equal(got, want) {
		t.Errorf("after ProcessQueue: methods=%v want %v", got, want)
	}
}

func TestXbmc_AlwaysUpdate_SkipsActivePlayerCheck(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) {
		s.AlwaysUpdate = true
		s.UpdateLibrary = true
	})
	msg := notifications.ImportCompleteMessage{Series: &series.Series{Path: "/srv/Show"}}
	_ = prov.OnImportComplete(t.Context(), msg)
	_ = prov.ProcessQueue(t.Context())
	for _, m := range methodsOf(capture) {
		if m == "Player.GetActivePlayers" {
			t.Errorf("Player.GetActivePlayers should not fire with AlwaysUpdate=true, got %v", methodsOf(capture))
		}
	}
}

func TestXbmc_VideoPlayerBusy_SkipsLibraryWork(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, map[string]string{
		"Player.GetActivePlayers": `{"result":[{"playerid":1,"type":"video"}],"id":1,"jsonrpc":"2.0"}`,
	}, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) {
		s.UpdateLibrary = true
		s.CleanLibrary = true
	})
	msg := notifications.ImportCompleteMessage{Series: &series.Series{Path: "/srv/Show"}}
	_ = prov.OnImportComplete(t.Context(), msg)
	_ = prov.ProcessQueue(t.Context())
	for _, m := range methodsOf(capture) {
		if m == "VideoLibrary.Scan" || m == "VideoLibrary.Clean" {
			t.Errorf("library work should be skipped while video plays, got %v", methodsOf(capture))
		}
	}
}

func TestXbmc_OnSeriesDelete_OnlyWhenDeletedFiles(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) { s.Notify = true })
	msg := notifications.SeriesDeleteMessage{Series: &series.Series{}, DeletedFiles: false}
	_ = prov.OnSeriesDelete(t.Context(), msg)
	if len(capture) != 0 {
		t.Errorf("DeletedFiles=false should be no-op, got %v", methodsOf(capture))
	}
}

func TestXbmc_BasicAuth_WhenUsernameSet(t *testing.T) {
	var capture []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capture = append(capture, captured{Method: r.Header.Get("Authorization"), Body: body})
		_, _ = w.Write([]byte(`{"result":"OK","id":1,"jsonrpc":"2.0"}`))
	}))
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) {
		s.Username = "u"
		s.Password = "p"
		s.Notify = true
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !strings.HasPrefix(capture[0].Method, "Basic ") {
		t.Errorf("expected Basic auth, got %q", capture[0].Method)
	}
}

func TestXbmc_JSONRPCErrorSurfacesTypedSentinel(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, map[string]string{
		"GUI.ShowNotification": `{"error":{"code":-32601,"message":"Method not found"},"id":1,"jsonrpc":"2.0"}`,
	}, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) { s.Notify = true })
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	var rpcErr *xbmc.ErrJSONRPC
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32601 {
		t.Errorf("err=%v want ErrJSONRPC code=-32601", err)
	}
}

func TestXbmc_500SurfacesSendFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) { s.Notify = true })
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, xbmc.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestXbmc_Test_SuccessRoundTrip(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, nil)
	failures, err := prov.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) != 0 {
		t.Errorf("expected zero failures, got %+v", failures)
	}
	if !strings.Contains(string(capture[0].Body), "Success! Kodi has been successfully configured!") {
		t.Errorf("test message missing")
	}
}

func TestXbmc_OnManualInteractionRequired_UsesPlainTitle(t *testing.T) {
	var capture []captured
	srv := rpcServer(t, nil, &capture)
	defer srv.Close()
	prov := makeXbmc(t, srv, func(s *xbmc.Settings) { s.Notify = true })
	_ = prov.OnManualInteractionRequired(t.Context(), notifications.ManualInteractionRequiredMessage{Message: "ok"})
	if !strings.Contains(string(capture[0].Body), `"title":"Manual Interaction"`) {
		t.Errorf("expected plain Manual Interaction title, got %s", string(capture[0].Body))
	}
}

func TestXbmc_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(xbmc.Implementation, xbmc.ConfigContract) {
		t.Errorf("registry missing %s/%s", xbmc.Implementation, xbmc.ConfigContract)
	}
}

// Use background ctx in OnRename test to ensure no-op path is fine.
func TestXbmc_OnRename_NoUpdateLibrary_IsNoop(t *testing.T) {
	srv := rpcServer(t, nil, &[]captured{})
	defer srv.Close()
	prov := makeXbmc(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{Series: &series.Series{}}); err != nil {
		t.Errorf("OnRename: %v", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
