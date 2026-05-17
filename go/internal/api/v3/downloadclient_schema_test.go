package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadClientSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	if got := len(downloadClientSchemas()); got != 20 {
		t.Errorf("downloadClientSchemas count: got %d want 20", got)
	}
}

func TestDownloadClientSchema_DownloadStationVariantsShareBaseFields(t *testing.T) {
	t.Parallel()
	// Both DownloadStation variants share the same configContract +
	// field shape — only protocol/implementation differ. Catches
	// drift if one side gets updated without the other.
	var torrent, usenet DownloadClientSchemaResource
	for _, s := range downloadClientSchemas() {
		if s.Implementation == "TorrentDownloadStation" {
			torrent = s
		}
		if s.Implementation == "UsenetDownloadStation" {
			usenet = s
		}
	}
	if torrent.ConfigContract != "DownloadStationSettings" || usenet.ConfigContract != "DownloadStationSettings" {
		t.Errorf("DownloadStation configContract: got %q/%q want both DownloadStationSettings",
			torrent.ConfigContract, usenet.ConfigContract)
	}
	if torrent.Protocol != "torrent" || usenet.Protocol != "usenet" {
		t.Errorf("DownloadStation protocols: got %q/%q want torrent/usenet",
			torrent.Protocol, usenet.Protocol)
	}
	if len(torrent.Fields) != len(usenet.Fields) {
		t.Errorf("DownloadStation field count drift: torrent=%d usenet=%d",
			len(torrent.Fields), len(usenet.Fields))
	}
}

func TestDownloadClientSchema_VuzeUsesTransmissionSettings(t *testing.T) {
	t.Parallel()
	// Vuze inherits Transmission's settings struct in C# — the
	// configContract MUST be TransmissionSettings, not VuzeSettings.
	// (This bit me during construction; pinning it explicitly.)
	for _, s := range downloadClientSchemas() {
		if s.Implementation != "Vuze" {
			continue
		}
		if s.ConfigContract != "TransmissionSettings" {
			t.Errorf("Vuze.configContract: got %q want TransmissionSettings", s.ConfigContract)
		}
		return
	}
	t.Errorf("Vuze missing from schema list")
}

func TestDownloadClientSchema_MessageBannersOnSixImpls(t *testing.T) {
	t.Parallel()
	wantMsg := map[string]string{
		"TorrentDownloadStation": "warning",
		"UsenetDownloadStation":  "warning",
		"Flood":                  "info",
		"RTorrent":               "info",
		"TriblerDownloadClient":  "warning",
		"UTorrent":               "warning",
	}
	for _, s := range downloadClientSchemas() {
		want, hasMsg := wantMsg[s.Implementation]
		if !hasMsg {
			if s.Message != nil {
				t.Errorf("%s: must NOT carry message, got %+v", s.Implementation, *s.Message)
			}
			continue
		}
		if s.Message == nil {
			t.Errorf("%s: missing required message banner", s.Implementation)
			continue
		}
		if s.Message.Type != want {
			t.Errorf("%s: message.type got %q want %q", s.Implementation, s.Message.Type, want)
		}
		if s.Message.Message == "" {
			t.Errorf("%s: message.message must be non-empty", s.Implementation)
		}
	}
}

func TestDownloadClientSchema_NamePresetsEnable(t *testing.T) {
	t.Parallel()
	// Every schema row must carry name="", presets=[], enable=false,
	// priority=1, removeCompletedDownloads=true, removeFailedDownloads=true.
	for _, s := range downloadClientSchemas() {
		if s.Name != "" {
			t.Errorf("%s: name must be empty on schema row, got %q", s.Implementation, s.Name)
		}
		if s.Presets == nil || len(s.Presets) != 0 {
			t.Errorf("%s: presets must be empty array, got %v", s.Implementation, s.Presets)
		}
		if s.Enable {
			t.Errorf("%s: enable must be false", s.Implementation)
		}
		if s.Priority != 1 {
			t.Errorf("%s: priority got %d want 1", s.Implementation, s.Priority)
		}
		if !s.RemoveCompletedDownloads || !s.RemoveFailedDownloads {
			t.Errorf("%s: remove flags got C=%v F=%v want both true", s.Implementation, s.RemoveCompletedDownloads, s.RemoveFailedDownloads)
		}
	}
}

func TestDownloadClientSchema_UTorrentInitialStateTypoPreserved(t *testing.T) {
	t.Parallel()
	// The C# `UTorrentSettings.IntialState` property name carries a
	// typo (missing the leading "I" → "intialState" on the wire).
	// Wire-shape compatibility means we must preserve it; this test
	// fires if anyone "fixes" it.
	for _, s := range downloadClientSchemas() {
		if s.Implementation != "UTorrent" {
			continue
		}
		for _, f := range s.Fields {
			if f.Name == "intialState" {
				return
			}
			if f.Name == "initialState" {
				t.Errorf("UTorrent: field name was corrected to 'initialState'; must remain 'intialState' (typo) per C# wire shape")
				return
			}
		}
		t.Errorf("UTorrent: intialState field missing")
		return
	}
}

func TestDownloadClientSchema_InfoLinkDerivation(t *testing.T) {
	t.Parallel()
	// InfoLink must be exactly lower(Implementation) suffixed to the
	// servarr-wiki base. Catches accidental drift.
	const base = "https://wiki.servarr.com/sonarr/supported#"
	for _, s := range downloadClientSchemas() {
		want := base + lowerASCII(s.Implementation)
		if s.InfoLink != want {
			t.Errorf("%s: infoLink got %q want %q", s.Implementation, s.InfoLink, want)
		}
	}
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func TestDownloadClientSchema_ContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	DownloadClientSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient/schema", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}

func TestDownloadClientSchema_MessageJSONOmittedWhenNil(t *testing.T) {
	t.Parallel()
	// Pin the omitempty semantic: impls without a message must NOT
	// emit the key at all (vs emitting null).
	rr := httptest.NewRecorder()
	DownloadClientSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/downloadclient/schema", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	hasMsg := map[string]bool{
		"TorrentDownloadStation": true, "UsenetDownloadStation": true,
		"Flood": true, "RTorrent": true,
		"TriblerDownloadClient": true, "UTorrent": true,
	}
	for _, r := range rows {
		impl := r["implementation"].(string)
		_, hasKey := r["message"]
		if hasMsg[impl] != hasKey {
			t.Errorf("%s: hasMessage=%v but JSON-key-present=%v", impl, hasMsg[impl], hasKey)
		}
	}
}
