package tags

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"sonarr.tv/sonarr/internal/messaging"
)

// TagsUpdatedEvent ports `TagsUpdatedEvent.cs:1-7`. Published by
// the Service after Add / Update / Delete operations so listeners
// (UI refresh, cache invalidations, etc.) can react.
type TagsUpdatedEvent struct{}

// Service ports the dependency-light surface of
// `src/NzbDrone.Core/Tags/TagService.cs`. Wraps the Tags Repo with
// domain rules and event publishing.
//
// What ships:
//
//   - GetByID(ctx, id), GetByLabel(ctx, label), GetByIDs(ctx, ids):
//     simple repo passthroughs.
//   - GetByString(ctx, s): C# `GetTag(string)` ergonomics — dispatches
//     to GetByID for all-digit input, GetByLabel otherwise.
//   - All(ctx) []Tag sorted by Label (mirrors C# `OrderBy(t => t.Label)`).
//   - Add(ctx, tag): existing-by-label dedupe + ToLowerInvariant +
//     Insert + Publish. Returns the existing or inserted Tag.
//   - Update(ctx, tag): ToLowerInvariant + Update + Publish.
//
// What's deferred (the cross-cutting C# methods):
//
//   - Details(int) / Details() — fan-outs across DelayProfile,
//     ImportList, Notification, ReleaseProfile, Series, Indexer,
//     AutoTag, DownloadClient services. Each lands when the
//     corresponding service ports its `AllForTag` query helper.
//   - Delete(int) — depends on Details() for the in-use rejection.
type Service struct {
	repo *Repo
	bus  *messaging.Bus
}

// NewService binds a Repo + Bus into a tag-management Service. The
// Bus is used to publish TagsUpdatedEvent; pass nil to skip
// publishing (useful for tests / migrations that run pre-event-bus).
func NewService(r *Repo, bus *messaging.Bus) *Service {
	return &Service{repo: r, bus: bus}
}

// GetByID returns the tag whose ID matches.
func (s *Service) GetByID(ctx context.Context, id int) (*Tag, error) {
	return s.repo.Get(ctx, id)
}

// GetByLabel returns the tag whose Label matches (UNIQUE).
func (s *Service) GetByLabel(ctx context.Context, label string) (*Tag, error) {
	return s.repo.GetByLabel(ctx, label)
}

// GetByString ports `TagService.GetTag(string)` at `:71-81`. When
// the input is all digits, dispatches to GetByID (parsed as int);
// otherwise dispatches to GetByLabel. Mirrors C# `tag.All(char.IsDigit)`.
func (s *Service) GetByString(ctx context.Context, input string) (*Tag, error) {
	if input != "" && allDigits(input) {
		id, err := strconv.Atoi(input)
		if err != nil {
			return nil, fmt.Errorf("tags: parse id %q: %w", input, err)
		}
		return s.GetByID(ctx, id)
	}
	return s.GetByLabel(ctx, input)
}

// GetByIDs ports `TagService.GetTags(IEnumerable<int>)` at `:83-86`.
// Calls Repo.Get for each id and collates results. Missing ids are
// skipped (NOT raised as errors) — mirrors the C# enumerable
// semantic where the LINQ `Get(ids)` could yield short of the
// input length without throwing.
func (s *Service) GetByIDs(ctx context.Context, ids []int) ([]Tag, error) {
	out := make([]Tag, 0, len(ids))
	for _, id := range ids {
		t, err := s.repo.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// All returns every tag, sorted by Label ascending. Mirrors
// `TagService.All` at `:153-156` which calls
// `_repo.All().OrderBy(t => t.Label)`.
func (s *Service) All(ctx context.Context) ([]Tag, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, err
	}
	sortByLabel(all)
	return all, nil
}

// Add ports `TagService.Add(Tag)` at `:158-173`. Dedupe-by-label
// FIRST: if a tag with the (lowercase) label already exists, return
// it without inserting. Otherwise lowercase the label, insert,
// publish TagsUpdatedEvent, return the inserted Tag.
//
// The C# `tag.Label.ToLowerInvariant()` happens AFTER the FindByLabel
// check; the lookup is exact-match against UNIQUE column so it
// would only match a previously-lowercased row. The Go port
// preserves that order verbatim.
func (s *Service) Add(ctx context.Context, tag *Tag) (*Tag, error) {
	if tag == nil {
		return nil, errors.New("tags: cannot add nil tag")
	}
	existing, err := s.repo.GetByLabel(ctx, tag.Label)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("tags: lookup before add: %w", err)
	}

	tag.Label = strings.ToLower(tag.Label)
	inserted, err := s.repo.Insert(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("tags: insert: %w", err)
	}
	s.publishUpdated(ctx)
	return inserted, nil
}

// Delete removes a tag by ID and publishes TagsUpdatedEvent. Mirrors
// `TagService.Delete(int tagId)` at
// `src/NzbDrone.Core/Tags/TagService.cs:185-189`. Returns ErrNotFound
// if the row does not exist (per the underlying repo.Delete contract).
//
// The C# `Delete` does NOT check Details() for in-use rejection — the
// V3 route exposes a plain DELETE that proceeds regardless; the
// front-end is responsible for pre-flighting with /tag/detail. The Go
// port preserves that posture for V3 wire compatibility.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("tags: delete %d: %w", id, err)
	}
	s.publishUpdated(ctx)
	return nil
}

// Update ports `TagService.Update(Tag)` at `:175-183`. Lowercases
// the label, persists, publishes TagsUpdatedEvent.
func (s *Service) Update(ctx context.Context, tag *Tag) error {
	if tag == nil {
		return errors.New("tags: cannot update nil tag")
	}
	tag.Label = strings.ToLower(tag.Label)
	if err := s.repo.Update(ctx, tag); err != nil {
		return fmt.Errorf("tags: update: %w", err)
	}
	s.publishUpdated(ctx)
	return nil
}

func (s *Service) publishUpdated(ctx context.Context) {
	if s.bus == nil {
		return
	}
	// Best-effort publish; subscriber errors don't surface to the
	// caller. Mirrors the C# event-aggregator fire-and-forget shape.
	_ = messaging.Publish(ctx, s.bus, TagsUpdatedEvent{})
}

func sortByLabel(tags []Tag) {
	// Insertion sort — Tag count is small (low tens typically) so
	// the simpler implementation beats reaching for sort.Slice.
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && tags[j-1].Label > tags[j].Label; j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
