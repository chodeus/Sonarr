package v3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/delayprofiles"
	"sonarr.tv/sonarr/internal/httperr"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/validation"
)

// DelayProfileResource mirrors
// `src/Sonarr.Api.V3/Profiles/Delay/DelayProfileResource.cs`.
//
// C# field shapes that need explicit handling:
//   - `preferredProtocol` is the DownloadProtocol enum, rendered by
//     JsonStringEnumConverter+CamelCase as the lower-case enum name
//     ("usenet" / "torrent" / "unknown"). The Go Profile stores the
//     int form; we resolve at the API edge via DownloadProtocolName.
//   - `minimumCustomFormatScore` is a non-nullable int in C# and is
//     always emitted as a number. The Go Profile carries *int (the
//     DB column is nullable); the mapper unwraps nil → 0 so the wire
//     shape always emits a number.
//   - `tags` is a HashSet<int> rendered as `[]` when empty. The
//     mapper nil-normalises to `[]int{}`.
type DelayProfileResource struct {
	ID                             int    `json:"id"`
	EnableUsenet                   bool   `json:"enableUsenet"`
	EnableTorrent                  bool   `json:"enableTorrent"`
	PreferredProtocol              string `json:"preferredProtocol"`
	UsenetDelay                    int    `json:"usenetDelay"`
	TorrentDelay                   int    `json:"torrentDelay"`
	BypassIfHighestQuality         bool   `json:"bypassIfHighestQuality"`
	BypassIfAboveCustomFormatScore bool   `json:"bypassIfAboveCustomFormatScore"`
	MinimumCustomFormatScore       int    `json:"minimumCustomFormatScore"`
	Order                          int    `json:"order"`
	Tags                           []int  `json:"tags"`
}

// DownloadProtocolName mirrors C# `JsonStringEnumConverter(JsonNamingPolicy
// .CamelCase)` applied to `DownloadProtocol` at
// `src/NzbDrone.Core/Indexers/DownloadProtocol.cs:3-9`. Three enum
// values map to lowercase names; anything outside that range falls
// back to "unknown" matching the C# zero-value.
//
// Exported so future indexer / queue / blocklist controllers reuse
// the mapping without duplicating the table.
func DownloadProtocolName(p indexers.DownloadProtocol) string {
	switch p {
	case indexers.DownloadProtocolUsenet:
		return "usenet"
	case indexers.DownloadProtocolTorrent:
		return "torrent"
	}
	return "unknown"
}

// DelayProfileLister is the read side the handler depends on.
// *delayprofiles.Service satisfies it via All.
type DelayProfileLister interface {
	All(ctx context.Context) ([]delayprofiles.Profile, error)
}

// DelayProfileHandler returns the http.Handler for
// GET /api/v3/delayprofile. Mirrors
// `DelayProfileController.GetAll` at
// `src/Sonarr.Api.V3/Profiles/Delay/DelayProfileController.cs:71-76`.
//
// Service.All preserves repo Order-by-Id ordering — the default
// global profile (Id=1, Order=INT_MAX) is the only row on a fresh
// DB.
func DelayProfileHandler(lister DelayProfileLister) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := lister.All(r.Context())
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		out := make([]DelayProfileResource, 0, len(rows))
		for _, row := range rows {
			out = append(out, toDelayProfileResource(row))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

func toDelayProfileResource(p delayprofiles.Profile) DelayProfileResource {
	tags := p.Tags
	if tags == nil {
		tags = []int{}
	}
	score := 0
	if p.MinimumCustomFormatScore != nil {
		score = *p.MinimumCustomFormatScore
	}
	return DelayProfileResource{
		ID:                             p.ID,
		EnableUsenet:                   p.EnableUsenet,
		EnableTorrent:                  p.EnableTorrent,
		PreferredProtocol:              DownloadProtocolName(indexers.DownloadProtocol(p.PreferredProtocol)),
		UsenetDelay:                    p.UsenetDelay,
		TorrentDelay:                   p.TorrentDelay,
		BypassIfHighestQuality:         p.BypassIfHighestQuality,
		BypassIfAboveCustomFormatScore: p.BypassIfAboveCustomFormatScore,
		MinimumCustomFormatScore:       score,
		Order:                          p.Order,
		Tags:                           tags,
	}
}

// ---------- 5.B8: CRUD + reorder ----------

// DelayProfileWriter is the write surface the CRUD + reorder handlers
// depend on. *delayprofiles.Service satisfies it via the existing
// All / Get / Add / Update / Delete / Reorder methods.
// DelayProfileController in C# extends RestController<T> (no SignalR).
type DelayProfileWriter interface {
	All(ctx context.Context) ([]delayprofiles.Profile, error)
	Get(ctx context.Context, id int) (*delayprofiles.Profile, error)
	Add(ctx context.Context, p *delayprofiles.Profile) (*delayprofiles.Profile, error)
	Update(ctx context.Context, p *delayprofiles.Profile) error
	Delete(ctx context.Context, id int) error
	Reorder(ctx context.Context, id int, afterID *int) ([]delayprofiles.Profile, error)
}

// DelayProfileByIDHandler returns GET /api/v3/delayprofile/{id}.
// Mirrors `DelayProfileController.GetResourceById` at
// `src/Sonarr.Api.V3/Profiles/Delay/DelayProfileController.cs:66-69`.
func DelayProfileByIDHandler(svc DelayProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDelayProfileID(w, r)
		if !ok {
			return
		}
		p, err := svc.Get(r.Context(), id)
		if errors.Is(err, delayprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("delayprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		writeDelayProfileJSON(w, http.StatusOK, *p)
	})
}

// DelayProfileCreateHandler returns POST /api/v3/delayprofile.
// Mirrors `Create` at `:36-44`. Validates per the C# SharedValidator
// chain before persisting.
func DelayProfileCreateHandler(svc DelayProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := decodeDelayProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("delayprofile: malformed body: %v", err))
			return
		}
		// On Create the body Id is unset (Service.Add assigns); Tags
		// rule applies the "non-empty for id != 1" branch — but for a
		// new profile the id isn't 1 yet (it'll be assigned on insert),
		// so use the body id (typically 0) for the rule. The rule fires
		// "tags not empty" when id != 1; id=0 on Create satisfies that.
		if errs := validateDelayProfile(body, false); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := fromDelayProfileResource(body)
		inserted, err := svc.Add(r.Context(), &model)
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		writeDelayProfileJSON(w, http.StatusCreated, *inserted)
	})
}

// DelayProfileUpdateHandler returns PUT /api/v3/delayprofile/{id}.
// Mirrors `Update` at `:57-64`. The id=1 path is the global default
// profile — Tags MUST be empty per the C# rule
// `.EmptyCollection<DelayProfileResource, int>().When(d => d.Id == 1)`.
func DelayProfileUpdateHandler(svc DelayProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDelayProfileID(w, r)
		if !ok {
			return
		}
		body, err := decodeDelayProfileBody(r)
		if err != nil {
			httperr.BadRequest(w, fmt.Sprintf("delayprofile: malformed body: %v", err))
			return
		}
		body.ID = id
		if errs := validateDelayProfile(body, true); !errs.OK() {
			httperr.WriteValidation(w, errs.Failures())
			return
		}
		model := fromDelayProfileResource(body)
		err = svc.Update(r.Context(), &model)
		if errors.Is(err, delayprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("delayprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		writeDelayProfileJSON(w, http.StatusAccepted, model)
	})
}

// DelayProfileDeleteHandler returns DELETE /api/v3/delayprofile/{id}.
// Mirrors `DeleteProfile` at `:46-55`. id=1 is the global default
// profile and CANNOT be deleted — emits 405 Method Not Allowed
// matching C# `throw new MethodNotAllowedException(...)`.
func DelayProfileDeleteHandler(svc DelayProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDelayProfileID(w, r)
		if !ok {
			return
		}
		if id == 1 {
			httperr.Write(w, http.StatusMethodNotAllowed, "Cannot delete global delay profile", "", nil)
			return
		}
		// Idempotent DELETE on a non-existent id — C.1.i-follow-4 alignment (J232).
		// C# returns 200 on missing id. The id==1 (global profile) case retains 405.
		if err := svc.Delete(r.Context(), id); err != nil && !errors.Is(err, delayprofiles.ErrNotFound) {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// DelayProfileReorderHandler returns PUT /api/v3/delayprofile/reorder/{id}.
// Mirrors `Reorder` at `:78-84`. Returns the reordered list of profiles.
// Query `after=<int>` is optional; when omitted (or empty), the
// profile moves to the top. When present, the profile is placed
// immediately after the row with that id.
func DelayProfileReorderHandler(svc DelayProfileWriter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDelayProfileID(w, r)
		if !ok {
			return
		}
		var afterID *int
		if raw := r.URL.Query().Get("after"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				httperr.BadRequest(w, fmt.Sprintf("delayprofile: invalid after %q", raw))
				return
			}
			afterID = &v
		}
		profiles, err := svc.Reorder(r.Context(), id, afterID)
		if errors.Is(err, delayprofiles.ErrNotFound) {
			httperr.NotFound(w, fmt.Sprintf("delayprofile %d not found", id))
			return
		}
		if err != nil {
			httperr.WriteErr(w, r, http.StatusInternalServerError, fmt.Errorf("delayprofiles: %w", err), nil)
			return
		}
		out := make([]DelayProfileResource, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, toDelayProfileResource(p))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(out)
		_, _ = w.Write(buf)
	})
}

// ---------- helpers ----------

func parseDelayProfileID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		httperr.BadRequest(w, "delayprofile: missing id path parameter")
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		httperr.BadRequest(w, fmt.Sprintf("delayprofile: invalid id %q", raw))
		return 0, false
	}
	return id, true
}

func decodeDelayProfileBody(r *http.Request) (*DelayProfileResource, error) {
	var t DelayProfileResource
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// validateDelayProfile ports the C# SharedValidator chain at
// `DelayProfileController.cs:21-33`:
//
//   - Tags NotEmpty when Id != 1
//   - Tags MUST be empty when Id == 1 (the global default profile)
//   - UsenetDelay >= 0
//   - TorrentDelay >= 0
//   - Either EnableUsenet OR EnableTorrent must be true
//
// SetValidator(tagInUseValidator) — checks that every tag id in the
// body is referenced by no OTHER delay profile — DEFERRED. Requires
// loading All() and walking tag overlaps; a follow-up slice can
// thread that through.
//
// isUpdate gates the id=1 special-case rule. On Create, the body
// id is typically 0 (DB assigns), so the "tags must be empty when
// id=1" branch only ever applies on Update.
func validateDelayProfile(res *DelayProfileResource, isUpdate bool) *validation.Errors {
	errs := validation.New()
	errs.NonNegative("UsenetDelay", res.UsenetDelay)
	errs.NonNegative("TorrentDelay", res.TorrentDelay)
	if !res.EnableUsenet && !res.EnableTorrent {
		errs.Add("EnableUsenet", "Either Usenet or Torrent should be enabled")
	}
	if isUpdate && res.ID == 1 {
		if len(res.Tags) > 0 {
			errs.Add("Tags", "Global delay profile must not have tags")
		}
	} else {
		if len(res.Tags) == 0 {
			errs.Add("Tags", "Tags are required for non-default profiles")
		}
	}
	return errs
}

// fromDelayProfileResource is the inverse of toDelayProfileResource.
// Handles the string-protocol → int-protocol mapping (the wire form
// uses the camelCase enum name; the persisted model stores the int)
// and the MinimumCustomFormatScore int → *int promotion (the C#
// model has a non-nullable int; the Go column is nullable, so we
// always pass through a *int even when the value is the default 0).
func fromDelayProfileResource(res *DelayProfileResource) delayprofiles.Profile {
	score := res.MinimumCustomFormatScore
	tags := res.Tags
	if tags == nil {
		tags = []int{}
	}
	return delayprofiles.Profile{
		ID:                             res.ID,
		EnableUsenet:                   res.EnableUsenet,
		EnableTorrent:                  res.EnableTorrent,
		PreferredProtocol:              int(downloadProtocolFromName(res.PreferredProtocol)),
		UsenetDelay:                    res.UsenetDelay,
		TorrentDelay:                   res.TorrentDelay,
		Order:                          res.Order,
		Tags:                           tags,
		BypassIfHighestQuality:         res.BypassIfHighestQuality,
		BypassIfAboveCustomFormatScore: res.BypassIfAboveCustomFormatScore,
		MinimumCustomFormatScore:       &score,
	}
}

// downloadProtocolFromName reverses DownloadProtocolName. Defaults
// to DownloadProtocolUnknown for unrecognised input (matches the C#
// JsonStringEnumConverter default behaviour on unknown values).
func downloadProtocolFromName(s string) indexers.DownloadProtocol {
	switch strings.ToLower(s) {
	case "usenet":
		return indexers.DownloadProtocolUsenet
	case "torrent":
		return indexers.DownloadProtocolTorrent
	}
	return indexers.DownloadProtocolUnknown
}

func writeDelayProfileJSON(w http.ResponseWriter, status int, p delayprofiles.Profile) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	buf, _ := json.Marshal(toDelayProfileResource(p))
	_, _ = w.Write(buf)
}
