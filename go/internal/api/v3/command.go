package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"sonarr.tv/sonarr/internal/commands"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/validation"
)

// CommandResource mirrors `src/Sonarr.Api.V3/Commands/CommandResource.cs`.
// 16 fields covering the persisted Command row + the body-derived
// projections (sendUpdatesToClient, updateScheduledTask,
// stateChangeTime, lastExecutionTime, clientUserAgent).
//
// Wire enum forms (camelCase):
//   - priority  : low | normal | high
//   - status    : queued | started | completed | failed | aborted |
//     cancelled | orphaned
//   - result    : unknown | successful | unsuccessful | indeterminate
//   - trigger   : unspecified | manual | scheduled
type CommandResource struct {
	ID                  int             `json:"id"`
	Name                string          `json:"name"`
	CommandName         string          `json:"commandName"`
	Message             string          `json:"message,omitempty"`
	Body                json.RawMessage `json:"body,omitempty"`
	Priority            string          `json:"priority"`
	Status              string          `json:"status"`
	Result              string          `json:"result"`
	Queued              time.Time       `json:"queued"`
	Started             *time.Time      `json:"started,omitempty"`
	Ended               *time.Time      `json:"ended,omitempty"`
	Duration            *string         `json:"duration,omitempty"`
	Exception           string          `json:"exception,omitempty"`
	Trigger             string          `json:"trigger"`
	ClientUserAgent     string          `json:"clientUserAgent,omitempty"`
	StateChangeTime     *time.Time      `json:"stateChangeTime,omitempty"`
	SendUpdatesToClient bool            `json:"sendUpdatesToClient"`
	UpdateScheduledTask bool            `json:"updateScheduledTask"`
	LastExecutionTime   *time.Time      `json:"lastExecutionTime,omitempty"`
}

// CommandService is the slice of *commands.Service the v3 handlers
// reach for. Defined locally for stubbable tests.
type CommandService interface {
	Push(ctx context.Context, name, body string, priority commands.Priority, trigger commands.Trigger) (*commands.Command, error)
	Get(ctx context.Context, id int) (*commands.Command, error)
	All(ctx context.Context) ([]commands.Command, error)
	Cancel(ctx context.Context, id int) error
}

// CommandNameResolver looks up the canonical Name registered with the
// dispatcher. Mirrors C# `KnownTypes.GetImplementations(typeof(Command))
// .Single(c => c.Name.Replace("Command","").Equals(resource.Name, …))`
// — the API accepts case-insensitive names, normalises to the
// registered casing, and rejects unknown names with 400.
//
// In production, *commands.CommandRegistry satisfies this via the
// adapter in cmd/sonarr/host_wiring.go.
type CommandNameResolver interface {
	CanonicalName(name string) (string, bool)
}

// commandPostBody is the inbound JSON shape. The full request body is
// re-encoded as Body before pushing to the queue so the handler sees
// every payload field the user supplied; only the envelope fields the
// REST handler cares about are pulled out here.
type commandPostBody struct {
	Name                string `json:"name"`
	SendUpdatesToClient *bool  `json:"sendUpdatesToClient,omitempty"`
}

// CommandCreateHandler returns POST /api/v3/command. Mirrors
// `CommandController.StartCommand` at `:54-79`:
//
//   - Parse resource.Name; reject blank with 400 (C# `NotBlank` validator).
//   - Look up the canonical command name via the registry; reject
//     unknown with 400 (C# `.Single()` throws InvalidOperationException
//     which surfaces as 500 in C#; the Go port returns a friendlier
//     400 with the deprecation-equivalent friendly message).
//   - Re-encode the raw body to canonical JSON and push it to the
//     queue with Priority=High for ManualImport, Normal otherwise.
//   - Always force SendUpdatesToClient=true; SuppressMessages mirrors
//     the inbound SendUpdatesToClient flag (DEFERRED in the Go port:
//     the body-flag projection requires a typed Command struct the
//     dispatcher doesn't expose yet).
//   - Return 201 + the persisted resource.
//
// DEFERRED: SuppressMessages/SendUpdatesToClient projection onto the
// body (the C# `command.SuppressMessages = !command.SendUpdatesToClient
// ; command.SendUpdatesToClient = true;` mutation is not modelled);
// ClientUserAgent capture from the User-Agent header (no infrastructure
// to thread the UA through to the persisted body yet); SignalR
// debounced fan-out via Handle(CommandUpdatedEvent) (mirrors the C#
// 100ms debouncer — Phase 5 follow-up).
func CommandCreateHandler(svc CommandService, names CommandNameResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("command: read body: %v", err))
			return
		}
		var envelope commandPostBody
		if err := json.Unmarshal(raw, &envelope); err != nil {
			httperr.BadRequest(w, fmt.Sprintf("command: malformed body: %v", err))
			return
		}
		errs := validation.New()
		if strings.TrimSpace(envelope.Name) == "" {
			errs.Add("Name", "Name cannot be blank")
		}
		if !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		canonical, ok := names.CanonicalName(envelope.Name)
		if !ok {
			httperr.WriteValidation(w, []httperr.Failure{{
				PropertyName: "Name",
				ErrorMessage: fmt.Sprintf("Unknown command %q", envelope.Name),
				Severity:     "error",
			}})
			return
		}
		priority := commands.PriorityNormal
		if canonical == "ManualImport" {
			priority = commands.PriorityHigh
		}
		// Normalise the persisted body's Name to the canonical casing
		// so dispatcher lookups round-trip cleanly. The user-sent body
		// becomes the persisted body — any additional fields the user
		// supplied (e.g. seriesIds for RefreshSeries) survive into the
		// queue row.
		bodyMap := map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &bodyMap); err != nil {
				httperr.BadRequest(w, fmt.Sprintf("command: malformed body: %v", err))
				return
			}
		}
		bodyMap["name"] = canonical
		canonicalBody, _ := json.Marshal(bodyMap)

		cmd, err := svc.Push(r.Context(), canonical, string(canonicalBody), priority, commands.TriggerManual)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("command: push: %w", err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		buf, _ := json.Marshal(commandToResource(*cmd))
		_, _ = w.Write(buf)
	})
}

// CommandListHandler returns GET /api/v3/command. Mirrors
// `CommandController.GetStartedCommands` at `:81-89`:
// All rows sorted by status (using CommandPriorityComparer: Started <
// Queued < everything-else, alphabetical fallback) then by priority
// descending.
func CommandListHandler(svc CommandService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		all, err := svc.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("command: all: %w", err), nil)
			return
		}
		sort.SliceStable(all, func(i, j int) bool {
			si, sj := statusSortRank(commands.Status(all[i].Status)), statusSortRank(commands.Status(all[j].Status))
			if si != sj {
				return si < sj
			}
			return all[i].Priority > all[j].Priority
		})
		out := make([]CommandResource, len(all))
		for i := range all {
			out[i] = commandToResource(all[i])
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// CommandByIDHandler returns GET /api/v3/command/{id}.
func CommandByIDHandler(svc CommandService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "command: invalid id")
			return
		}
		cmd, err := svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, commands.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("command %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("command: get %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(commandToResource(*cmd))
		_, _ = w.Write(buf)
	})
}

// CommandDeleteHandler returns DELETE /api/v3/command/{id}. Mirrors
// `CommandController.CancelCommand` at `:91-95`. C# returns 200 with
// no body via `void`; the Go port returns 200 with an empty JSON
// object to match the standard REST envelope.
func CommandDeleteHandler(svc CommandService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil || id <= 0 {
			httperr.BadRequest(w, "command: invalid id")
			return
		}
		if err := svc.Cancel(r.Context(), id); err != nil {
			if errors.Is(err, commands.ErrNotFound) {
				httperr.NotFound(w, fmt.Sprintf("command %d not found", id))
				return
			}
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("command: cancel %d: %w", id, err), nil)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
}

// statusSortRank mirrors C# CommandPriorityComparer:
//
//	Started (1) → 0
//	Queued  (0) → 1
//	everything else → 2 (alphabetical fallback collapses to enum order)
//
// The C# comparer is custom-coded; the rank function captures the same
// "show running first, then queued, then history" ordering.
func statusSortRank(s commands.Status) int {
	switch s {
	case commands.StatusStarted:
		return 0
	case commands.StatusQueued:
		return 1
	}
	return 2
}

// commandToResource maps a persisted commands.Command into the wire
// shape. Duration is rendered as the C# TimeSpan format
// (hh:mm:ss[.fffffff]) via datastore.FormatDuration; nil duration
// stays nil. Body is rendered as json.RawMessage from the persisted
// Body string (which is canonical JSON the API produced on Push).
func commandToResource(c commands.Command) CommandResource {
	res := CommandResource{
		ID:          c.ID,
		Name:        c.Name,
		CommandName: splitCamelCase(c.Name),
		Priority:    CamelCaseEnumName(commands.Priority(c.Priority).String()),
		Status:      CamelCaseEnumName(commands.Status(c.Status).String()),
		Result:      CamelCaseEnumName(commands.Result(c.Result).String()),
		Queued:      c.QueuedAt,
		Started:     c.StartedAt,
		Ended:       c.EndedAt,
		Exception:   c.Exception,
		Trigger:     CamelCaseEnumName(commands.Trigger(c.Trigger).String()),
	}
	if c.Duration != nil {
		s := formatDuration(*c.Duration)
		res.Duration = &s
	}
	if c.Body != "" {
		res.Body = json.RawMessage(c.Body)
	}
	switch {
	case c.StartedAt != nil:
		res.StateChangeTime = c.StartedAt
	case c.EndedAt != nil:
		res.StateChangeTime = c.EndedAt
	}
	return res
}

// splitCamelCase ports `string.SplitCamelCase()` (NzbDrone.Common
// extension): inserts a space before each uppercase letter that
// follows a lowercase letter or a digit. "RssSync" → "Rss Sync";
// "CheckForFinishedDownload" → "Check For Finished Download".
func splitCamelCase(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev := rune(s[i-1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				b.WriteRune(' ')
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// formatDuration renders a Go time.Duration as the C# TimeSpan format
// (hh:mm:ss[.fffffff]). Negative durations are emitted with a leading
// minus sign; the fractional component is rendered with up to 7-digit
// precision matching .NET TimeSpan.ToString().
func formatDuration(d time.Duration) string {
	neg := d < 0
	if neg {
		d = -d
	}
	hours := int64(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int64(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int64(d / time.Second)
	d -= time.Duration(seconds) * time.Second
	nanos := d.Nanoseconds()
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
	fmt.Fprintf(&sb, "%02d:%02d:%02d", hours, minutes, seconds)
	if nanos > 0 {
		// .NET TimeSpan uses 7-digit ticks (100ns). Trim trailing zeros.
		ticks := nanos / 100
		frac := fmt.Sprintf(".%07d", ticks)
		frac = strings.TrimRight(frac, "0")
		if frac != "." {
			sb.WriteString(frac)
		}
	}
	return sb.String()
}
