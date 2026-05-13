package twitter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/twitter"
)

type captured struct {
	URL         string
	AuthHeader  string
	ContentType string
	Body        []byte
	ParsedBody  url.Values
}

func capturingServer(t *testing.T, statusCode int, capture *[]captured) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))
		*capture = append(*capture, captured{
			URL:         r.URL.Path,
			AuthHeader:  r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
			ParsedBody:  vals,
		})
		if statusCode > 0 {
			w.WriteHeader(statusCode)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func settingsJSON(t *testing.T, s twitter.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeTwitter(t *testing.T, srv *httptest.Server, extras func(*twitter.Settings)) notifications.Provider {
	t.Helper()
	s := twitter.Settings{
		ConsumerKey:       "ck",
		ConsumerSecret:    "cs",
		AccessToken:       "at",
		AccessTokenSecret: "ats",
		Mention:           "sonarr",
		DirectMessage:     true,
	}
	if extras != nil {
		extras(&s)
	}
	twitter.SetAPIURLsForTest(srv.URL+"/1.1/statuses/update.json", srv.URL+"/1.1/direct_messages/new.json")
	t.Cleanup(func() {
		twitter.SetAPIURLsForTest(
			"https://api.twitter.com/1.1/statuses/update.json",
			"https://api.twitter.com/1.1/direct_messages/new.json",
		)
	})
	def := &notifications.Notification{
		Implementation: twitter.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := twitter.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    twitter.Settings
		want string
	}{
		{"NoCK", twitter.Settings{ConsumerSecret: "x", AccessToken: "x", AccessTokenSecret: "x"}, "ConsumerKey"},
		{"NoCS", twitter.Settings{ConsumerKey: "x", AccessToken: "x", AccessTokenSecret: "x"}, "ConsumerSecret"},
		{"NoAT", twitter.Settings{ConsumerKey: "x", ConsumerSecret: "x", AccessTokenSecret: "x"}, "AccessToken"},
		{"NoATS", twitter.Settings{ConsumerKey: "x", ConsumerSecret: "x", AccessToken: "x"}, "AccessTokenSecret"},
		{"DMRequiresMention", twitter.Settings{
			ConsumerKey: "x", ConsumerSecret: "x", AccessToken: "x", AccessTokenSecret: "x",
			DirectMessage: true,
		}, "Mention"},
		{"OK", twitter.Settings{
			ConsumerKey: "x", ConsumerSecret: "x", AccessToken: "x", AccessTokenSecret: "x",
		}, ""},
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

func TestTwitter_DirectMessage_PostsToDMEndpoint(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(capture) != 1 {
		t.Fatalf("calls=%d", len(capture))
	}
	if capture[0].URL != "/1.1/direct_messages/new.json" {
		t.Errorf("url=%q", capture[0].URL)
	}
	if !strings.HasPrefix(capture[0].AuthHeader, "OAuth ") {
		t.Errorf("expected OAuth header, got %q", capture[0].AuthHeader)
	}
	// Body has the body params; signature is in the header.
	if !strings.Contains(string(capture[0].Body), "text=Grabbed") {
		t.Errorf("missing body text param: %q", string(capture[0].Body))
	}
	if !strings.Contains(string(capture[0].Body), "screenname=sonarr") {
		t.Errorf("missing screenname param: %q", string(capture[0].Body))
	}
}

func TestTwitter_PublicStatus_AppendsMention(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, func(s *twitter.Settings) {
		s.DirectMessage = false
		s.Mention = "sonarr"
	})
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if capture[0].URL != "/1.1/statuses/update.json" {
		t.Errorf("expected status endpoint, got %q", capture[0].URL)
	}
	// percentEncode encodes "@" as %40; verify the appended mention.
	if !strings.Contains(string(capture[0].Body), "%40sonarr") {
		t.Errorf("missing @mention in body: %q", string(capture[0].Body))
	}
}

func TestTwitter_OAuthHeaderContainsExpectedFields(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	auth := capture[0].AuthHeader
	for _, want := range []string{
		"oauth_consumer_key=\"ck\"",
		"oauth_token=\"at\"",
		"oauth_signature_method=\"HMAC-SHA1\"",
		"oauth_version=\"1.0\"",
		"oauth_signature=\"",
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("Authorization header missing %q: %s", want, auth)
		}
	}
}

func TestTwitter_OnRename_IsNoop(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("OnRename should not POST")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestTwitter_Test_BadAuthSurfacesFailure(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusUnauthorized, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Host" {
		t.Errorf("expected Host failure, got %+v", failures)
	}
}

func TestTwitter_500SurfacesSendFailed(t *testing.T) {
	var capture []captured
	srv := capturingServer(t, http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if !errors.Is(err, twitter.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestTwitter_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(twitter.Implementation, twitter.ConfigContract) {
		t.Errorf("registry missing %s/%s", twitter.Implementation, twitter.ConfigContract)
	}
}

// oauthHandshakeServer dispatches /oauth/request_token and
// /oauth/access_token. requestBody is the form-encoded body returned
// for /oauth/request_token; accessBody for /oauth/access_token. If
// status is nonzero, applies to BOTH endpoints.
func oauthHandshakeServer(t *testing.T, requestBody, accessBody string, status int, capture *[]captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*capture = append(*capture, captured{
			URL:        r.URL.Path,
			AuthHeader: r.Header.Get("Authorization"),
			Body:       body,
		})
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		switch r.URL.Path {
		case "/oauth/request_token":
			_, _ = w.Write([]byte(requestBody))
		case "/oauth/access_token":
			_, _ = w.Write([]byte(accessBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	twitter.SetOAuthURLsForTest(
		srv.URL+"/oauth/request_token",
		srv.URL+"/oauth/access_token",
		srv.URL+"/oauth/authorize",
	)
	t.Cleanup(func() {
		twitter.SetOAuthURLsForTest(
			"https://api.twitter.com/oauth/request_token",
			"https://api.twitter.com/oauth/access_token",
			"https://api.twitter.com/oauth/authorize",
		)
	})
	return srv
}

func TestTwitter_RequestAction_StartOAuth_BuildsAuthorizeURL(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t,
		"oauth_token=req-tok&oauth_token_secret=req-sec&oauth_callback_confirmed=true",
		"", 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "startOAuth", map[string]string{"callbackUrl": "http://cb/ret"})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	urlStr, _ := m["oauthUrl"].(string)
	if !strings.HasSuffix(urlStr, "/oauth/authorize?oauth_token=req-tok") {
		t.Errorf("oauthUrl=%q want authorize?oauth_token=req-tok suffix", urlStr)
	}
	// Verify the request_token call was OAuth1-signed and carried
	// oauth_callback in the Authorization header.
	if len(capture) != 1 || capture[0].URL != "/oauth/request_token" {
		t.Fatalf("expected single /oauth/request_token call, got %+v", capture)
	}
	auth := capture[0].AuthHeader
	if !strings.HasPrefix(auth, "OAuth ") {
		t.Errorf("Authorization=%q lacks OAuth prefix", auth)
	}
	if !strings.Contains(auth, `oauth_callback="http%3A%2F%2Fcb%2Fret"`) {
		t.Errorf("Authorization missing percent-encoded oauth_callback: %s", auth)
	}
	if !strings.Contains(auth, `oauth_signature=`) {
		t.Errorf("Authorization missing oauth_signature: %s", auth)
	}
}

func TestTwitter_RequestAction_StartOAuth_MissingCallback(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t, "", "", 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	_, err := prov.RequestAction(t.Context(), "startOAuth", map[string]string{})
	if !errors.Is(err, twitter.ErrBadRequest) {
		t.Errorf("err=%v want ErrBadRequest", err)
	}
}

func TestTwitter_RequestAction_GetOAuthToken_ResolvesTokenPair(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t,
		"",
		"oauth_token=final-tok&oauth_token_secret=final-sec&screen_name=sonarr",
		0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "getOAuthToken", map[string]string{
		"oauth_token":    "tmp-tok",
		"oauth_verifier": "verify-123",
	})
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	if m["accessToken"] != "final-tok" {
		t.Errorf("accessToken=%v", m["accessToken"])
	}
	if m["accessTokenSecret"] != "final-sec" {
		t.Errorf("accessTokenSecret=%v", m["accessTokenSecret"])
	}
	// Verify the access_token call carried oauth_verifier + the
	// temporary oauth_token in the Authorization header.
	if len(capture) != 1 || capture[0].URL != "/oauth/access_token" {
		t.Fatalf("expected single /oauth/access_token call, got %+v", capture)
	}
	auth := capture[0].AuthHeader
	if !strings.Contains(auth, `oauth_verifier="verify-123"`) {
		t.Errorf("Authorization missing oauth_verifier: %s", auth)
	}
	if !strings.Contains(auth, `oauth_token="tmp-tok"`) {
		t.Errorf("Authorization missing temporary oauth_token: %s", auth)
	}
}

func TestTwitter_RequestAction_GetOAuthToken_MissingParams(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t, "", "", 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	cases := []map[string]string{
		{"oauth_verifier": "v"}, // missing oauth_token
		{"oauth_token": "t"},    // missing oauth_verifier
	}
	for i, q := range cases {
		_, err := prov.RequestAction(t.Context(), "getOAuthToken", q)
		if !errors.Is(err, twitter.ErrBadRequest) {
			t.Errorf("case %d: err=%v want ErrBadRequest", i, err)
		}
	}
}

func TestTwitter_RequestAction_StartOAuth_ServerErrorPropagates(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t, "", "", http.StatusInternalServerError, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	_, err := prov.RequestAction(t.Context(), "startOAuth", map[string]string{"callbackUrl": "http://cb"})
	if !errors.Is(err, twitter.ErrSendFailed) {
		t.Errorf("err=%v want ErrSendFailed", err)
	}
}

func TestTwitter_RequestAction_UnknownActionReturnsEmpty(t *testing.T) {
	var capture []captured
	srv := oauthHandshakeServer(t, "", "", 0, &capture)
	defer srv.Close()
	prov := makeTwitter(t, srv, nil)
	out, err := prov.RequestAction(t.Context(), "bogus", nil)
	if err != nil {
		t.Fatalf("RequestAction: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("expected empty map, got %T %+v", out, out)
	}
}
