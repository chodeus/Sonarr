package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/indexers"
)

type fakeDelayProfileLister struct {
	rows []delayprofiles.Profile
	err  error
}

func (f *fakeDelayProfileLister) All(context.Context) ([]delayprofiles.Profile, error) {
	return f.rows, f.err
}

func TestDownloadProtocolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   indexers.DownloadProtocol
		want string
	}{
		{indexers.DownloadProtocolUnknown, "unknown"},
		{indexers.DownloadProtocolUsenet, "usenet"},
		{indexers.DownloadProtocolTorrent, "torrent"},
		{indexers.DownloadProtocol(99), "unknown"},
	}
	for _, tc := range cases {
		if got := DownloadProtocolName(tc.in); got != tc.want {
			t.Errorf("DownloadProtocolName(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDelayProfileHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: nil}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestDelayProfileHandler_DefaultProfile(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: []delayprofiles.Profile{
		{
			ID: 1, EnableUsenet: true, EnableTorrent: true,
			PreferredProtocol:      int(indexers.DownloadProtocolUsenet),
			Order:                  2147483647,
			BypassIfHighestQuality: true,
			// MinimumCustomFormatScore = nil → must emit 0
			Tags: nil, // → []
		},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`"id":1`,
		`"enableUsenet":true`,
		`"enableTorrent":true`,
		`"preferredProtocol":"usenet"`,
		`"usenetDelay":0`,
		`"torrentDelay":0`,
		`"bypassIfHighestQuality":true`,
		`"bypassIfAboveCustomFormatScore":false`,
		`"minimumCustomFormatScore":0`,
		`"order":2147483647`,
		`"tags":[]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}

func TestDelayProfileHandler_NonNilMinScore(t *testing.T) {
	t.Parallel()
	score := 75
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{rows: []delayprofiles.Profile{
		{ID: 2, PreferredProtocol: int(indexers.DownloadProtocolTorrent), MinimumCustomFormatScore: &score},
	}}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"minimumCustomFormatScore":75`) {
		t.Errorf("expected score 75: %s", body)
	}
	if !strings.Contains(body, `"preferredProtocol":"torrent"`) {
		t.Errorf("expected torrent protocol: %s", body)
	}
}

func TestDelayProfileHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DelayProfileHandler(&fakeDelayProfileLister{err: errors.New("db down")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/delayprofile", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
