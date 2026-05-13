package mediabrowser_test

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
	"sonarr.tv/sonarr/internal/notifications/mediabrowser"
	"sonarr.tv/sonarr/internal/series"
)

type captured struct {
	Method string
	Path   string
	Token  string
	Body   []byte
}

func capturingServer(t *testing.T, routes map[string]int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			Method: r.Method,
			Path:   r.URL.Path,
			Token:  r.Header.Get("X-MediaBrowser-Token"),
			Body:   body,
		})
		if code, ok := routes[r.URL.Path]; ok && code > 0 {
			w.WriteHeader(code)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s mediabrowser.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeMB(t *testing.T, srv *httptest.Server, extras func(*mediabrowser.Settings)) notifications.Provider {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s := mediabrowser.Settings{
		Host:          u.Hostname(),
		Port:          port,
		ApiKey:        "key-xyz",
		UpdateLibrary: true,
	}
	if extras != nil {
		extras(&s)
	}
	def := &notifications.Notification{
		Implementation: mediabrowser.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := mediabrowser.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    mediabrowser.Settings
		want string
	}{
		{"NoHost", mediabrowser.Settings{ApiKey: "x"}, "Host"},
		{"NoApiKey", mediabrowser.Settings{Host: "h"}, "ApiKey"},
		{"MapFromWithoutMapTo", mediabrowser.Settings{Host: "h", ApiKey: "x", MapFrom: "/a"}, "MapTo"},
		{"MapToWithoutMapFrom", mediabrowser.Settings{Host: "h", ApiKey: "x", MapTo: "/b"}, "MapFrom"},
		{"OK", mediabrowser.Settings{Host: "h", ApiKey: "x"}, ""},
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

func TestMB_OnDownload_NotifyImmediatelyAndQueuesUpdate(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, func(s *mediabrowser.Settings) { s.Notify = true })
	msg := notifications.DownloadMessage{
		Series: &series.Series{Path: "/srv/Show"},
	}
	msg.Message = "ok"
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	// Notify fires synchronously; the library-update is queued for
	// the next ProcessQueue tick (mirrors C# MediaServerUpdateQueue).
	if len(capture) != 1 {
		t.Fatalf("after OnDownload: calls=%d want 1 (notify only)", len(capture))
	}
	if capture[0].Path != "/Notifications/Admin" {
		t.Errorf("notify path=%q", capture[0].Path)
	}
	if capture[0].Token != "key-xyz" {
		t.Errorf("token=%q", capture[0].Token)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if len(capture) != 2 {
		t.Fatalf("after ProcessQueue: calls=%d want 2", len(capture))
	}
	if capture[1].Path != "/Library/Media/Updated" {
		t.Errorf("update path=%q", capture[1].Path)
	}
	// Single accumulated update-type should round-trip into the body.
	if !strings.Contains(string(capture[1].Body), `"UpdateType":"Created"`) {
		t.Errorf("expected Created, got %s", string(capture[1].Body))
	}
}

func TestMB_NotifyDisabled_OnlyUpdateFires(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	// OnSeriesAdd only queues the update — nothing fires until
	// ProcessQueue.
	if len(capture) != 0 {
		t.Fatalf("OnSeriesAdd should have queued only, got %+v", capture)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if len(capture) != 1 || capture[0].Path != "/Library/Media/Updated" {
		t.Errorf("calls=%+v", capture)
	}
}

func TestMB_UpdateLibraryDisabled_OnlyNotifyFires(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, func(s *mediabrowser.Settings) {
		s.UpdateLibrary = false
		s.Notify = true
	})
	msg := notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	if len(capture) != 1 || capture[0].Path != "/Notifications/Admin" {
		t.Errorf("calls=%+v", capture)
	}
}

func TestMB_PathMapping_AppliedAtDrain(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, func(s *mediabrowser.Settings) {
		s.MapFrom = "/srv/series"
		s.MapTo = "/media/tv"
	})
	msg := notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/series/Show"}}
	_ = prov.OnSeriesAdd(t.Context(), msg)
	_ = prov.ProcessQueue(t.Context())
	if len(capture) != 1 {
		t.Fatalf("calls=%d", len(capture))
	}
	body := string(capture[0].Body)
	if !strings.Contains(body, `"Path":"/media/tv/Show"`) {
		t.Errorf("expected mapped path /media/tv/Show, got %s", body)
	}
}

func TestMB_OnRename_QueuesModifiedUpdate(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	msg := notifications.RenameMessage{Series: &series.Series{Path: "/srv/Show"}}
	if err := prov.OnRename(t.Context(), msg); err != nil {
		t.Fatalf("OnRename: %v", err)
	}
	if len(capture) != 0 {
		t.Fatalf("OnRename should defer the update to ProcessQueue, got %d immediate calls", len(capture))
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls=%d", len(capture))
	}
	if !strings.Contains(string(capture[0].Body), `"UpdateType":"Modified"`) {
		t.Errorf("expected Modified update-type, got %s", string(capture[0].Body))
	}
}

func TestMB_OnManualInteractionRequired_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnManualInteractionRequired should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	if err := prov.OnManualInteractionRequired(context.Background(), notifications.ManualInteractionRequiredMessage{}); err != nil {
		t.Errorf("OnManualInteractionRequired should be a no-op, got %v", err)
	}
	if prov.SupportsOnManualInteractionRequired() {
		t.Error("SupportsOnManualInteractionRequired should be false")
	}
}

func TestMB_Test_401MapsToApiKey(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, map[string]int{"/System/Configuration": http.StatusUnauthorized}, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "ApiKey" {
		t.Errorf("expected ApiKey failure, got %+v", failures)
	}
}

func TestMB_Test_Notify404MapsToJellyfinAdvice(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, map[string]int{"/Notifications/Admin": http.StatusNotFound}, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, func(s *mediabrowser.Settings) { s.Notify = true })
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Notify" {
		t.Errorf("expected Notify failure, got %+v", failures)
	}
}

func TestMB_500SurfacesSendFailedFromProcessQueue(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, map[string]int{"/Library/Media/Updated": http.StatusInternalServerError}, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	msg := notifications.SeriesAddMessage{Series: &series.Series{Path: "/srv/Show"}}
	if err := prov.OnSeriesAdd(t.Context(), msg); err != nil {
		t.Fatalf("OnSeriesAdd should queue without error, got %v", err)
	}
	err := prov.ProcessQueue(t.Context())
	if !errors.Is(err, mediabrowser.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed (drain-time failure)", err)
	}
}

func TestMB_OnDownloadThenOnSeriesDelete_CoalescesToEmptyUpdateType(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, nil, &capture)
	defer srv.Close()
	prov := makeMB(t, srv, nil)
	ser := &series.Series{ID: 17, Path: "/srv/Show"}
	if err := prov.OnDownload(t.Context(), notifications.DownloadMessage{Series: ser}); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	if err := prov.OnSeriesDelete(t.Context(), notifications.SeriesDeleteMessage{Series: ser}); err != nil {
		t.Fatalf("OnSeriesDelete: %v", err)
	}
	if err := prov.ProcessQueue(t.Context()); err != nil {
		t.Fatalf("ProcessQueue: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls=%d want 1 coalesced", len(capture))
	}
	// Multiple distinct UpdateTypes accumulated against the same
	// series ⇒ C# `:130` emits null/empty so Emby decides.
	if !strings.Contains(string(capture[0].Body), `"UpdateType":""`) {
		t.Errorf("expected empty UpdateType for multi-info coalesce, got %s", string(capture[0].Body))
	}
}

func TestMB_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(mediabrowser.Implementation, mediabrowser.ConfigContract) {
		t.Errorf("registry missing %s/%s", mediabrowser.Implementation, mediabrowser.ConfigContract)
	}
}
