package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotificationSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	if got := len(notificationSchemas()); got != 25 {
		t.Errorf("notificationSchemas count: got %d want 25", got)
	}
}

func TestNotificationSchema_AllOnFlagsFalse(t *testing.T) {
	t.Parallel()
	// Every schema row must have all 13 on* fields false (schemas
	// don't subscribe to any event by default; user opts in on save).
	for _, s := range notificationSchemas() {
		if s.OnApplicationUpdate || s.OnDownload || s.OnEpisodeFileDelete ||
			s.OnEpisodeFileDeleteForUpgrade || s.OnGrab || s.OnHealthIssue ||
			s.OnHealthRestored || s.OnImportComplete ||
			s.OnManualInteractionRequired || s.OnRename || s.OnSeriesAdd ||
			s.OnSeriesDelete || s.OnUpgrade {
			t.Errorf("%s: on* flags must all be false on schemas", s.Implementation)
		}
	}
}

func TestNotificationSchema_SupportsOnImportCompleteUniformlyTrue(t *testing.T) {
	t.Parallel()
	// All 25 impls support OnImportComplete (the newest event added
	// to the wire — uniform per-impl); regression-pin so a future
	// per-impl override doesn't accidentally drop it.
	for _, s := range notificationSchemas() {
		if !s.SupportsOnImportComplete {
			t.Errorf("%s: supportsOnImportComplete must be true", s.Implementation)
		}
	}
}

func TestNotificationSchema_PlexServerAndSynologyIndexerSupportsMatrix(t *testing.T) {
	t.Parallel()
	// PlexServer + SynologyIndexer share the same 5-false supportsOn*
	// pattern: OnGrab / OnHealthIssue / OnHealthRestored /
	// OnApplicationUpdate / OnManualInteractionRequired all false.
	for _, s := range notificationSchemas() {
		if s.Implementation != "PlexServer" && s.Implementation != "SynologyIndexer" {
			continue
		}
		if s.SupportsOnGrab || s.SupportsOnHealthIssue ||
			s.SupportsOnHealthRestored || s.SupportsOnApplicationUpdate ||
			s.SupportsOnManualInteractionRequired {
			t.Errorf("%s: must have OnGrab/Health*/AppUpdate/ManualInteraction supports=false", s.Implementation)
		}
	}
}

func TestNotificationSchema_TraktNoGrabNoHealthNoRename(t *testing.T) {
	t.Parallel()
	for _, s := range notificationSchemas() {
		if s.Implementation != "Trakt" {
			continue
		}
		if s.SupportsOnGrab || s.SupportsOnRename || s.SupportsOnHealthIssue ||
			s.SupportsOnHealthRestored || s.SupportsOnApplicationUpdate ||
			s.SupportsOnManualInteractionRequired {
			t.Errorf("Trakt: must have Grab/Rename/Health*/AppUpdate/ManualInteraction supports=false")
		}
		return
	}
	t.Errorf("Trakt missing")
}

func TestNotificationSchema_CustomScriptHasMessage(t *testing.T) {
	t.Parallel()
	// CustomScript is the ONLY impl that carries a static message
	// banner (warning about EventType=Test on test runs).
	for _, s := range notificationSchemas() {
		if s.Implementation == "CustomScript" {
			if s.Message == nil {
				t.Fatal("CustomScript must carry a message")
			}
			if s.Message.Type != "warning" {
				t.Errorf("CustomScript message.type: got %q want warning", s.Message.Type)
			}
			if !strings.Contains(s.Message.Message, "EventType set to Test") {
				t.Errorf("CustomScript message.message must mention EventType=Test, got %q", s.Message.Message)
			}
			continue
		}
		if s.Message != nil {
			t.Errorf("%s: must NOT carry a message, got %+v", s.Implementation, *s.Message)
		}
	}
}

func TestNotificationSchema_SchemaDefaults(t *testing.T) {
	t.Parallel()
	for _, s := range notificationSchemas() {
		if s.Name != "" {
			t.Errorf("%s: name must be empty, got %q", s.Implementation, s.Name)
		}
		if s.IncludeHealthWarnings {
			t.Errorf("%s: includeHealthWarnings must default false", s.Implementation)
		}
		if s.Presets == nil || len(s.Presets) != 0 {
			t.Errorf("%s: presets must be empty array", s.Implementation)
		}
		if s.Tags == nil || len(s.Tags) != 0 {
			t.Errorf("%s: tags must be empty array", s.Implementation)
		}
	}
}

func TestNotificationSchema_InfoLinkDerivation(t *testing.T) {
	t.Parallel()
	const base = "https://wiki.servarr.com/sonarr/supported#"
	for _, s := range notificationSchemas() {
		want := base + lowerASCII(s.Implementation)
		if s.InfoLink != want {
			t.Errorf("%s: infoLink got %q want %q", s.Implementation, s.InfoLink, want)
		}
	}
}

func TestNotificationSchema_MailgunConfigContractLowercase(t *testing.T) {
	t.Parallel()
	// MailGun (implementation) → MailgunSettings (lowercase 'g' in
	// the Settings type name). Easy to typo as MailGunSettings.
	for _, s := range notificationSchemas() {
		if s.Implementation != "MailGun" {
			continue
		}
		if s.ConfigContract != "MailgunSettings" {
			t.Errorf("MailGun.configContract: got %q want MailgunSettings", s.ConfigContract)
		}
		return
	}
	t.Errorf("MailGun missing")
}

func TestNotificationSchema_NtfyImplementationNameLowercase(t *testing.T) {
	t.Parallel()
	// Ntfy is the only impl whose ImplementationName diverges from
	// its Implementation in case — "ntfy.sh" vs "Ntfy". Catches
	// drift if anyone "fixes" the casing.
	for _, s := range notificationSchemas() {
		if s.Implementation != "Ntfy" {
			continue
		}
		if s.ImplementationName != "ntfy.sh" {
			t.Errorf("Ntfy.implementationName: got %q want ntfy.sh", s.ImplementationName)
		}
		return
	}
	t.Errorf("Ntfy missing")
}

func TestNotificationSchema_MessageJSONOmittedWhenNil(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification/schema", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		impl := r["implementation"].(string)
		_, hasKey := r["message"]
		wantKey := impl == "CustomScript"
		if hasKey != wantKey {
			t.Errorf("%s: message-key-present=%v want=%v", impl, hasKey, wantKey)
		}
	}
}

func TestNotificationSchema_ContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification/schema", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}
