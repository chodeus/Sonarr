package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/notifications"
)

// TestPayload_DownloadFieldsOmitWhenEmpty is the J172 byte-shape
// guard. C# `WebhookBase.cs:45-47/69-71/100-102/210-212` sources the
// `DownloadClient` / `DownloadClientType` / `DownloadId` properties
// from `message.DownloadClientName` (Grab) or
// `message.DownloadClientInfo?.Name` / `?.Type` (Download /
// ImportComplete / ManualInteraction) — all C# nullable `string`
// reference-type properties that default to null when no download-
// client info is available (e.g. manual-import grabs, manual
// interaction events fired without a tracked download).
//
// `WebhookProxy.SendWebhook` at `WebhookProxy.cs:39` calls
// `body.ToJson()` which routes through Newtonsoft's `ToJson`
// extension at `src/NzbDrone.Common/Serializer/Newtonsoft.Json/
// Json.cs:119` using the shared `SerializerSettings` with
// `NullValueHandling = NullValueHandling.Ignore` at `Json.cs:26`,
// so the three null properties are OMITTED from the wire body.
// The Go-port's Go-string zero value is `""` rather than null, so
// without `omitempty` a zero-value Go payload would emit
// `"downloadClient":""` etc. — a wire-shape divergence. The flag
// drops those keys when empty, matching C#.
func TestPayload_DownloadFieldsOmitWhenEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload any
	}{
		{name: "GrabPayload", payload: GrabPayload{BasePayload: BasePayload{EventType: EventTypeGrab}}},
		{name: "ImportPayload", payload: ImportPayload{BasePayload: BasePayload{EventType: EventTypeDownload}}},
		{name: "ImportCompletePayload", payload: ImportCompletePayload{BasePayload: BasePayload{EventType: EventTypeDownload}}},
		{name: "ManualInteractionPayload", payload: ManualInteractionPayload{BasePayload: BasePayload{EventType: EventTypeManualInteractionRequired}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal %s: %v", tc.name, err)
			}
			got := string(b)
			for _, key := range []string{`"downloadClient"`, `"downloadClientType"`, `"downloadId"`} {
				if strings.Contains(got, key) {
					t.Errorf("%s with empty DC fields must omit %s; got %s", tc.name, key, got)
				}
			}
		})
	}
}

// TestBuildDownloadPayload_DeletedFilesOmitWhenEmpty is the J173
// byte-shape guard for the OldFiles.Any() conditional. C#
// `WebhookBase.cs:75-82` wraps the DeletedFiles assignment in
// `if (message.OldFiles.Any())`, so an empty OldFiles leaves the
// property at its `List<>` reference-type default (null) which
// `NullValueHandling.Ignore` drops. The Go-port's
// [BuildDownloadPayload] now mirrors that conditional: when
// `len(msg.OldFiles) == 0` it leaves DeletedFiles as the zero
// `any` value (nil interface), at which point the `omitempty` tag
// drops the key.
//
// The tag alone is insufficient: an `any` field holding a typed-nil
// slice marshals to `"deletedFiles":null` (because the interface
// carries a type so `reflect.Interface.IsNil()` reports false),
// and an empty non-nil slice marshals to `"deletedFiles":[]`. Only
// a true nil-interface assignment plus omitempty produces the
// C# parity omit.
func TestBuildDownloadPayload_DeletedFilesOmitWhenEmpty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		oldFiles []*episodefiles.DeletedFile
	}{
		{name: "nil OldFiles slice", oldFiles: nil},
		{name: "empty non-nil OldFiles slice", oldFiles: []*episodefiles.DeletedFile{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := BuildDownloadPayload(notifications.DownloadMessage{OldFiles: tc.oldFiles})
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got := string(b)
			if strings.Contains(got, `"deletedFiles"`) {
				t.Errorf("Download body with empty OldFiles must omit deletedFiles; got %s", got)
			}
		})
	}
}

// TestBuildDownloadPayload_DeletedFilesEmitWhenPopulated confirms
// the opposite branch: when OldFiles has entries, the key appears
// with a populated array — the C# `OldFiles.Any()` true branch.
func TestBuildDownloadPayload_DeletedFilesEmitWhenPopulated(t *testing.T) {
	msg := notifications.DownloadMessage{
		OldFiles: []*episodefiles.DeletedFile{{}},
	}
	p := BuildDownloadPayload(msg)
	if p.DeletedFiles == nil {
		t.Fatalf("DeletedFiles must be populated when OldFiles is non-empty; got nil")
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"deletedFiles"`) {
		t.Errorf("Download body with non-empty OldFiles must include deletedFiles; got %s", b)
	}
}

// TestPayload_DownloadFieldsEmitWhenSet confirms the opposite branch:
// populated DownloadClient / DownloadClientType / DownloadID DO
// appear on the wire (omitempty only suppresses the zero value).
func TestPayload_DownloadFieldsEmitWhenSet(t *testing.T) {
	p := GrabPayload{
		BasePayload:        BasePayload{EventType: EventTypeGrab},
		DownloadClient:     "Sabnzbd",
		DownloadClientType: "Sabnzbd",
		DownloadID:         "ab12cd34",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"downloadClient":"Sabnzbd"`,
		`"downloadClientType":"Sabnzbd"`,
		`"downloadId":"ab12cd34"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("populated GrabPayload must contain %s; got %s", want, got)
		}
	}
}
