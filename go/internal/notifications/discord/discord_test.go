package discord_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/healthcheck"
	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/discord"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/series"
)

func settingsJSON(t *testing.T, s discord.Settings) string {
	t.Helper()
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(out)
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		url      string
		wantFail bool
	}{
		{"valid https", "https://discord.com/api/webhooks/123/abc", false},
		{"empty", "", true},
		{"no scheme", "discord.com/api/webhooks/123", true},
		{"unsupported scheme", "ftp://example.com/hook", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := discord.Settings{WebHookURL: c.url}
			failures := s.Validate()
			if c.wantFail && len(failures) == 0 {
				t.Errorf("expected validation failure for %q", c.url)
			}
			if !c.wantFail && len(failures) > 0 {
				t.Errorf("unexpected failure for %q: %+v", c.url, failures)
			}
			if c.wantFail && len(failures) > 0 && failures[0].PropertyName != "WebHookUrl" {
				t.Errorf("PropertyName: got %q want WebHookUrl", failures[0].PropertyName)
			}
		})
	}
}

// TestDefaultSettings verifies the C# constructor defaults at
// DiscordSettings.cs:18-66 — every Grab/Import/ManualInteraction
// field is selected by default.
func TestDefaultSettings(t *testing.T) {
	t.Parallel()
	s := discord.DefaultSettings()
	if len(s.GrabFields) != 13 {
		t.Errorf("GrabFields len = %d, want 13", len(s.GrabFields))
	}
	if len(s.ImportFields) != 13 {
		t.Errorf("ImportFields len = %d, want 13", len(s.ImportFields))
	}
	if len(s.ManualInteractionFields) != 10 {
		t.Errorf("ManualInteractionFields len = %d, want 10", len(s.ManualInteractionFields))
	}
}

func TestDiscord_Test_SuccessRoundTrip(t *testing.T) {
	t.Parallel()

	var (
		gotMethod      string
		gotContentType string
		gotBody        []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	p, err := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(failures) > 0 {
		t.Errorf("Test failures: %+v", failures)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q", gotContentType)
	}
	var payload discord.Payload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(payload.Embeds))
	}
	if payload.Embeds[0].Color != int(discord.ColorSuccess) {
		t.Errorf("Test embed color: got %d want ColorSuccess(%d)", payload.Embeds[0].Color, discord.ColorSuccess)
	}
	if !strings.Contains(payload.Embeds[0].Description, "Test") {
		t.Errorf("Description: got %q", payload.Embeds[0].Description)
	}
}

func TestDiscord_Test_NonSuccessReturnsFailure(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad token"}`))
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	failures, err := p.Test(t.Context())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}
	if failures[0].PropertyName != "WebHookUrl" {
		t.Errorf("PropertyName: got %q want WebHookUrl", failures[0].PropertyName)
	}
}

// TestDiscord_OnHealthIssue verifies the Warning/Error/Notice color
// mapping at Discord.cs:506-509.
func TestDiscord_OnHealthIssue_ColorMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level     healthcheck.Result
		wantColor int
	}{
		{healthcheck.ResultError, int(discord.ColorDanger)},
		{healthcheck.ResultWarning, int(discord.ColorWarning)},
		{healthcheck.ResultNotice, int(discord.ColorWarning)},
	}
	for _, c := range cases {
		t.Run(c.level.String(), func(t *testing.T) {
			t.Parallel()
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)
			def := &notifications.Notification{
				Implementation: discord.Implementation,
				Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
			}
			p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
			hc := healthcheck.HealthCheck{
				Source:  "ApiKeyCheck",
				Type:    c.level,
				Message: "API key is weak",
				WikiURL: "https://wiki.servarr.com/sonarr/system#api-key",
			}
			if err := p.OnHealthIssue(t.Context(), notifications.HealthCheckMessage{Check: hc}); err != nil {
				t.Fatalf("OnHealthIssue: %v", err)
			}
			var payload discord.Payload
			_ = json.Unmarshal(gotBody, &payload)
			if len(payload.Embeds) != 1 {
				t.Fatalf("expected 1 embed")
			}
			emb := payload.Embeds[0]
			if emb.Color != c.wantColor {
				t.Errorf("Color: got %d want %d", emb.Color, c.wantColor)
			}
			if emb.Title != "API key is weak" {
				t.Errorf("Title: got %q want %q", emb.Title, "API key is weak")
			}
			if emb.Description != "Health Check Failure" {
				t.Errorf("Description: got %q", emb.Description)
			}
		})
	}
}

// TestDiscord_OnHealthRestored always uses Success color, mirrors
// C# Discord.cs:522-551.
func TestDiscord_OnHealthRestored(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err := p.OnHealthRestored(t.Context(), notifications.HealthCheckMessage{Check: healthcheck.HealthCheck{Source: "Foo", Type: healthcheck.ResultOK}}); err != nil {
		t.Fatalf("OnHealthRestored: %v", err)
	}
	var payload discord.Payload
	_ = json.Unmarshal(gotBody, &payload)
	if len(payload.Embeds) != 1 || payload.Embeds[0].Color != int(discord.ColorSuccess) {
		t.Errorf("OnHealthRestored: want Success color")
	}
	if payload.Embeds[0].Description != "Health Check Restored" {
		t.Errorf("Description: got %q", payload.Embeds[0].Description)
	}
}

// TestDiscord_OnSeriesAdd verifies the title + tvdb URL are
// populated from the series record.
func TestDiscord_OnSeriesAdd(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	s := &series.Series{Title: "My Show", TvdbID: 12345}
	if err := p.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{Series: s}); err != nil {
		t.Fatalf("OnSeriesAdd: %v", err)
	}
	var payload discord.Payload
	_ = json.Unmarshal(gotBody, &payload)
	if len(payload.Embeds) != 1 {
		t.Fatalf("expected 1 embed")
	}
	emb := payload.Embeds[0]
	if emb.Title != "My Show" {
		t.Errorf("Title: got %q want 'My Show'", emb.Title)
	}
	if emb.URL != "http://thetvdb.com/?tab=series&id=12345" {
		t.Errorf("URL: got %q", emb.URL)
	}
	if emb.Description != "Series Added" {
		t.Errorf("Description: got %q", emb.Description)
	}
}

// TestDiscord_OnDownload_UpgradeFlipsColor verifies the upgrade
// color arm at Discord.cs:184.
func TestDiscord_OnDownload_UpgradeFlipsColor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		oldFiles  int
		wantColor int
		wantDesc  string
	}{
		{"new file", 0, int(discord.ColorStandard), "Episode Imported"},
		{"upgrade", 1, int(discord.ColorUpgrade), "Episode Imported Upgrade"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(srv.Close)

			def := &notifications.Notification{
				Implementation: discord.Implementation,
				Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
			}
			p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
			msg := notifications.DownloadMessage{Series: &series.Series{Title: "T"}}
			for i := 0; i < c.oldFiles; i++ {
				msg.OldFiles = append(msg.OldFiles, nil)
			}
			if err := p.OnDownload(t.Context(), msg); err != nil {
				t.Fatalf("OnDownload: %v", err)
			}
			var payload discord.Payload
			_ = json.Unmarshal(gotBody, &payload)
			if payload.Embeds[0].Color != c.wantColor {
				t.Errorf("Color: got %d want %d", payload.Embeds[0].Color, c.wantColor)
			}
			if payload.Embeds[0].Description != c.wantDesc {
				t.Errorf("Description: got %q want %q", payload.Embeds[0].Description, c.wantDesc)
			}
		})
	}
}

// TestDiscord_UsernameAndAvatarPropagate verifies Settings.Username
// + Settings.Avatar are forwarded as the Discord webhook override
// fields. Mirrors C# Payload.Username + Payload.AvatarUrl
// population.
func TestDiscord_UsernameAndAvatarPropagate(t *testing.T) {
	t.Parallel()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings: settingsJSON(t, discord.Settings{
			WebHookURL: srv.URL,
			Username:   "Sonarr-Bot",
			Avatar:     "https://example.com/avatar.png",
		}),
	}
	p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	_, _ = p.Test(t.Context())

	var payload discord.Payload
	_ = json.Unmarshal(gotBody, &payload)
	if payload.Username != "Sonarr-Bot" {
		t.Errorf("Username: got %q want Sonarr-Bot", payload.Username)
	}
	if payload.AvatarURL != "https://example.com/avatar.png" {
		t.Errorf("AvatarURL: got %q", payload.AvatarURL)
	}
}

// TestDiscord_New_BadSettingsJSON surfaces the deserialisation
// failure as a typed error.
func TestDiscord_New_BadSettingsJSON(t *testing.T) {
	t.Parallel()
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       "not json",
	}
	_, err := discord.New(def, notifications.Dependencies{})
	if err == nil {
		t.Errorf("expected unmarshal error")
	}
}

func TestDiscord_New_NilDefinition(t *testing.T) {
	t.Parallel()
	_, err := discord.New(nil, notifications.Dependencies{})
	if err == nil {
		t.Errorf("expected nil-def error")
	}
}

// TestDiscord_RegisteredInDefault verifies the init() registration
// landed.
func TestDiscord_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(discord.Implementation, discord.ConfigContract) {
		t.Errorf("Discord not registered in notifications.Default")
	}
}

// TestDiscord_SendFailedSurfacesError verifies non-2xx wraps as
// ErrSendFailed at the OnX-hook boundary.
func TestDiscord_SendFailedSurfacesError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	p, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	err := p.OnSeriesAdd(t.Context(), notifications.SeriesAddMessage{})
	if err == nil {
		t.Fatalf("expected send error")
	}
	if !errors.Is(err, discord.ErrSendFailed) {
		t.Errorf("error not ErrSendFailed: %v", err)
	}
}

// ===========================================================================
// Slice 4.CA (C-cleanup [C5]) field-rendering tests.
// ===========================================================================

func TestDiscord_OnGrab_FieldRenderingReachableSubset(t *testing.T) {
	t.Parallel()
	var payload discord.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	settings := discord.DefaultSettings()
	settings.WebHookURL = srv.URL
	// Limit GrabFields to the reachable subset so the assertions match.
	settings.GrabFields = []int{
		int(discord.GrabFieldOverview),
		int(discord.GrabFieldQuality),
		int(discord.GrabFieldGroup),
		int(discord.GrabFieldSize),
		int(discord.GrabFieldRelease),
		int(discord.GrabFieldCustomFormats),
		int(discord.GrabFieldCustomFormatScore),
		int(discord.GrabFieldIndexer),
	}

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	prov, err := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	q := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}
	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 7, Title: "Show", TvdbID: 99}
	sub.Episodes = []episodes.Episode{{
		ID: 100, SeasonNumber: 1, EpisodeNumber: 5,
		Overview: "Pilot episode where many things happen.",
	}}
	sub.ParsedEpisodeInfo = &parser.ParsedEpisodeInfo{
		Quality:      q,
		ReleaseGroup: "RELGRP",
	}
	sub.Release = &parser.ReleaseInfo{
		Title:   "Show.S01E05.720p.WEB-DL.RELGRP",
		Size:    1_500_000_000, // ≈1.4 GiB
		Indexer: "IdxA",
	}
	sub.CustomFormats = []customformats.CustomFormat{
		{ID: 1, Name: "x265"},
		{ID: 2, Name: "Repack"},
	}
	sub.CustomFormatScore = 42

	msg := notifications.GrabMessage{
		Series:  sub.Series,
		Episode: sub,
		Quality: &q,
	}
	if err := prov.OnGrab(t.Context(), msg); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}

	embed := payload.Embeds[0]
	got := fieldMap(embed.Fields)
	wantName := []string{"Overview", "Quality", "Group", "Size", "Release", "Custom Formats", "Custom Format Score", "Indexer"}
	for _, n := range wantName {
		if _, ok := got[n]; !ok {
			t.Errorf("missing field %q; got names %v", n, fieldNames(embed.Fields))
		}
	}
	// Spot-check a few values.
	if v := got["Quality"]; v.Value != q.Quality.Name {
		t.Errorf("Quality.Value = %q, want %q", v.Value, q.Quality.Name)
	}
	if v := got["Group"]; v.Value != "RELGRP" {
		t.Errorf("Group.Value = %q, want RELGRP", v.Value)
	}
	if v := got["Indexer"]; v.Value != "IdxA" {
		t.Errorf("Indexer.Value = %q, want IdxA", v.Value)
	}
	if v := got["Custom Formats"]; v.Value != "x265|Repack" {
		t.Errorf("Custom Formats.Value = %q, want x265|Repack", v.Value)
	}
	if v := got["Custom Format Score"]; v.Value != "42" {
		t.Errorf("Custom Format Score.Value = %q, want 42", v.Value)
	}
	if v := got["Release"]; !strings.Contains(v.Value, "```") || !strings.Contains(v.Value, "Show.S01E05.720p.WEB-DL.RELGRP") {
		t.Errorf("Release.Value = %q, want triple-backticked release title", v.Value)
	}
	// Size should be ~1.40 GB (1_500_000_000 / 1024^3 = 1.40 GiB).
	if v := got["Size"]; !strings.Contains(v.Value, "GB") {
		t.Errorf("Size.Value = %q, want a GB value", v.Value)
	}
	if v := got["Quality"]; !v.Inline {
		t.Error("Quality should be inline")
	}
	if v := got["Size"]; !v.Inline {
		t.Error("Size should be inline")
	}
}

func TestDiscord_OnGrab_SilentlySkipsDeferredFields(t *testing.T) {
	t.Parallel()
	var payload discord.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	settings := discord.DefaultSettings()
	settings.WebHookURL = srv.URL
	// Include ONLY the upstream-blocked field types to verify they
	// silently skip without erroring or producing Field entries.
	settings.GrabFields = []int{
		int(discord.GrabFieldPoster),
		int(discord.GrabFieldFanart),
		int(discord.GrabFieldRating),
		int(discord.GrabFieldGenres),
		int(discord.GrabFieldLinks),
	}
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	prov, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	sub := decisionengine.NewSubject()
	sub.Series = &series.Series{ID: 1, Title: "X"}
	sub.Episodes = []episodes.Episode{{}}
	sub.Release = &parser.ReleaseInfo{Title: "x.S01E01"}
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Series: sub.Series, Episode: sub}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(payload.Embeds[0].Fields) != 0 {
		t.Errorf("deferred-only fields should produce empty Fields, got %+v", payload.Embeds[0].Fields)
	}
}

func TestDiscord_OnDownload_RendersReachableEpisodeFileFields(t *testing.T) {
	t.Parallel()
	var payload discord.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	settings := discord.DefaultSettings()
	settings.WebHookURL = srv.URL
	settings.ImportFields = []int{
		int(discord.ImportFieldQuality),
		int(discord.ImportFieldGroup),
		int(discord.ImportFieldSize),
		int(discord.ImportFieldRelease),
	}
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	prov, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})

	q := quality.Model{Quality: quality.HDTV720p, Revision: quality.NewRevision()}
	qJSON, _ := json.Marshal(q)
	file := &episodefiles.File{
		Size:         800_000_000,
		Quality:      string(qJSON),
		ReleaseGroup: "GRP",
		SceneName:    "Show.S01E02.SCENE",
	}
	msg := notifications.DownloadMessage{
		Series:      &series.Series{ID: 1, Title: "Show"},
		EpisodeFile: file,
	}
	if err := prov.OnDownload(t.Context(), msg); err != nil {
		t.Fatalf("OnDownload: %v", err)
	}
	got := fieldMap(payload.Embeds[0].Fields)
	for _, n := range []string{"Quality", "Group", "Size", "Release"} {
		if _, ok := got[n]; !ok {
			t.Errorf("missing field %q; got %v", n, fieldNames(payload.Embeds[0].Fields))
		}
	}
	if got["Group"].Value != "GRP" {
		t.Errorf("Group=%q", got["Group"].Value)
	}
	if !strings.Contains(got["Release"].Value, "Show.S01E02.SCENE") {
		t.Errorf("Release=%q", got["Release"].Value)
	}
}

func TestDiscord_OnImportComplete_RendersReachableFields(t *testing.T) {
	t.Parallel()
	var payload discord.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	settings := discord.DefaultSettings()
	settings.WebHookURL = srv.URL
	settings.ImportFields = []int{
		int(discord.ImportFieldQuality),
		int(discord.ImportFieldGroup),
		int(discord.ImportFieldSize),
	}
	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, settings),
	}
	prov, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})

	q := quality.Model{Quality: quality.WEBDL720p, Revision: quality.NewRevision()}
	msg := notifications.ImportCompleteMessage{
		Series:         &series.Series{ID: 1, Title: "Show"},
		ReleaseQuality: &q,
		ReleaseGroup:   "RELGRP",
		EpisodeFiles: []*episodefiles.File{
			{Size: 500_000_000},
			{Size: 300_000_000},
		},
	}
	if err := prov.OnImportComplete(t.Context(), msg); err != nil {
		t.Fatalf("OnImportComplete: %v", err)
	}
	got := fieldMap(payload.Embeds[0].Fields)
	if got["Group"].Value != "RELGRP" {
		t.Errorf("Group=%q", got["Group"].Value)
	}
	if got["Quality"].Value != q.Quality.Name {
		t.Errorf("Quality=%q want %q", got["Quality"].Value, q.Quality.Name)
	}
	// Size = sum of EpisodeFile sizes = 800_000_000 → ~762 MB.
	if !strings.Contains(got["Size"].Value, "MB") {
		t.Errorf("Size=%q, want MB value", got["Size"].Value)
	}
}

func TestDiscord_OnEpisodeFileDelete_AddsFileNameField(t *testing.T) {
	t.Parallel()
	var payload discord.Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	def := &notifications.Notification{
		Implementation: discord.Implementation,
		Settings:       settingsJSON(t, discord.Settings{WebHookURL: srv.URL}),
	}
	prov, _ := discord.New(def, notifications.Dependencies{HTTPClient: srv.Client()})
	msg := notifications.EpisodeDeleteMessage{
		Series:      &series.Series{ID: 1, Title: "X"},
		EpisodeFile: &episodefiles.File{RelativePath: "Show/Season 01/Show.S01E01.mkv"},
	}
	if err := prov.OnEpisodeFileDelete(t.Context(), msg); err != nil {
		t.Fatalf("OnEpisodeFileDelete: %v", err)
	}
	got := fieldMap(payload.Embeds[0].Fields)
	v, ok := got["File name"]
	if !ok {
		t.Fatalf("missing File name field; got %v", fieldNames(payload.Embeds[0].Fields))
	}
	if !strings.Contains(v.Value, "Show/Season 01/Show.S01E01.mkv") {
		t.Errorf("File name value = %q", v.Value)
	}
}

// --- helpers ---

func fieldMap(fields []discord.Field) map[string]discord.Field {
	out := make(map[string]discord.Field, len(fields))
	for _, f := range fields {
		out[f.Name] = f
	}
	return out
}

func fieldNames(fields []discord.Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}
