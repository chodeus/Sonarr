package email_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/notifications/email"
)

type captured struct {
	From       string
	Recipients []string
	Raw        []byte
}

type fakeDialer struct {
	calls []captured
	err   error
}

func (f *fakeDialer) Send(_ context.Context, _ *email.Settings, from string, recipients []string, raw []byte) error {
	f.calls = append(f.calls, captured{From: from, Recipients: append([]string(nil), recipients...), Raw: append([]byte(nil), raw...)})
	return f.err
}

func settingsJSON(t *testing.T, s email.Settings) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func makeEmail(t *testing.T, extras func(*email.Settings)) (notifications.Provider, *fakeDialer) {
	t.Helper()
	s := email.Settings{
		Server: "smtp.example.com",
		Port:   587,
		From:   "sonarr@example.com",
		To:     []string{"a@x.com"},
	}
	if extras != nil {
		extras(&s)
	}
	fd := &fakeDialer{}
	email.SetDialerForTest(fd)
	t.Cleanup(func() { email.SetDialerForTest(nil) })
	def := &notifications.Notification{
		Implementation: email.Implementation,
		Settings:       settingsJSON(t, s),
	}
	p, err := email.New(def, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, fd
}

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		s    email.Settings
		want string
	}{
		{"NoServer", email.Settings{Port: 587, From: "a@x.com", To: []string{"b@x.com"}}, "Server"},
		{"BadPort", email.Settings{Server: "s", Port: 70000, From: "a@x.com", To: []string{"b@x.com"}}, "Port"},
		{"NoFrom", email.Settings{Server: "s", Port: 587, To: []string{"b@x.com"}}, "From"},
		{"NoRecipients", email.Settings{Server: "s", Port: 587, From: "a@x.com"}, "To"},
		{"BadToEmail", email.Settings{Server: "s", Port: 587, From: "a@x.com", To: []string{"notanemail"}}, "To"},
		{"OKWithBccOnly", email.Settings{Server: "s", Port: 587, From: "a@x.com", Bcc: []string{"b@x.com"}}, ""},
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

func TestEmail_OnGrab_SendsMessage(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	if err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "Show.S01E01"}); err != nil {
		t.Fatalf("OnGrab: %v", err)
	}
	if len(fd.calls) != 1 {
		t.Fatalf("calls = %d", len(fd.calls))
	}
	if fd.calls[0].From != "sonarr@example.com" {
		t.Errorf("From=%q", fd.calls[0].From)
	}
	if len(fd.calls[0].Recipients) != 1 || fd.calls[0].Recipients[0] != "a@x.com" {
		t.Errorf("Recipients=%v", fd.calls[0].Recipients)
	}
	raw := string(fd.calls[0].Raw)
	if !strings.Contains(raw, "Subject: Sonarr - Episode Grabbed") {
		t.Errorf("missing Subject: %q", raw)
	}
	if !strings.Contains(raw, "Show.S01E01 sent to queue.") {
		t.Errorf("missing body suffix: %q", raw)
	}
	if !strings.Contains(raw, "From: sonarr@example.com") {
		t.Errorf("missing From header: %q", raw)
	}
	if !strings.Contains(raw, "To: a@x.com") {
		t.Errorf("missing To header: %q", raw)
	}
}

func TestEmail_OnImportComplete_PlainTitleAndBodyWrap(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	if err := prov.OnImportComplete(t.Context(), notifications.ImportCompleteMessage{Message: "Show"}); err != nil {
		t.Fatalf("OnImportComplete: %v", err)
	}
	raw := string(fd.calls[0].Raw)
	if !strings.Contains(raw, "Subject: Import Complete") {
		t.Errorf("Subject should be plain 'Import Complete', got %q", raw)
	}
	if !strings.Contains(raw, "All expected episode files in Show downloaded and sorted.") {
		t.Errorf("missing wrapped body: %q", raw)
	}
}

func TestEmail_OnEpisodeFileDelete_AppendsDeletedSuffix(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	if err := prov.OnEpisodeFileDelete(t.Context(), notifications.EpisodeDeleteMessage{Message: "Ep1"}); err != nil {
		t.Fatalf("OnEpisodeFileDelete: %v", err)
	}
	raw := string(fd.calls[0].Raw)
	if !strings.Contains(raw, "Ep1 deleted.") {
		t.Errorf("missing deleted suffix: %q", raw)
	}
}

func TestEmail_ToCcBcc_FanOut_PutsAllInRecipients_ButOnlyToCcInHeaders(t *testing.T) {
	prov, fd := makeEmail(t, func(s *email.Settings) {
		s.Cc = []string{"cc@x.com"}
		s.Bcc = []string{"bcc@x.com"}
	})
	_ = prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	rcpts := fd.calls[0].Recipients
	if len(rcpts) != 3 {
		t.Fatalf("recipients=%v want 3", rcpts)
	}
	raw := string(fd.calls[0].Raw)
	if !strings.Contains(raw, "Cc: cc@x.com") {
		t.Errorf("missing Cc header: %q", raw)
	}
	if strings.Contains(raw, "Bcc:") {
		t.Errorf("Bcc header should NOT appear in raw message: %q", raw)
	}
}

func TestEmail_OnRename_IsNoop(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	if err := prov.OnRename(context.Background(), notifications.RenameMessage{}); err != nil {
		t.Errorf("OnRename should be a no-op, got %v", err)
	}
	if len(fd.calls) != 0 {
		t.Errorf("OnRename should not send, got %+v", fd.calls)
	}
	if prov.SupportsOnRename() {
		t.Error("SupportsOnRename should be false")
	}
}

func TestEmail_Test_DialerFailureMapsToServerProperty(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	fd.err = errors.New("connection refused")
	failures, _ := prov.Test(t.Context())
	if len(failures) == 0 || failures[0].PropertyName != "Server" {
		t.Errorf("expected Server failure, got %+v", failures)
	}
}

func TestEmail_DialerSendFailedPropagatesAsError(t *testing.T) {
	prov, fd := makeEmail(t, nil)
	fd.err = errors.New("smtp 535")
	err := prov.OnGrab(t.Context(), notifications.GrabMessage{Message: "x"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestEmail_RegisteredInDefault(t *testing.T) {
	t.Parallel()
	if !notifications.Default().Has(email.Implementation, email.ConfigContract) {
		t.Errorf("registry missing %s/%s", email.Implementation, email.ConfigContract)
	}
}
