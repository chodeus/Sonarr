package updatehistory

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"sonarr.tv/sonarr/internal/lifecycle"
)

// repo narrows the Repo surface the Service depends on, so tests can
// inject a stub. *Repo satisfies it.
type repo interface {
	Insert(ctx context.Context, h *History) error
	LastInstalled(ctx context.Context) (*History, error)
	PreviouslyInstalled(ctx context.Context) (*History, error)
	InstalledSince(ctx context.Context, dateTime time.Time) ([]History, error)
	Purge(ctx context.Context) error
}

// Service ports C# `UpdateHistoryService` (`Update/History/
// UpdateHistoryService.cs`). It records the running version on
// application start and exposes the previously-installed version for
// the recent-updates feed.
//
// The C# service additionally gates recording on
// `_configFileProvider.LogDbEnabled`; in the Go tree the Service is
// constructed only when the log DB is enabled (its repo lives in the
// RoleLog DB), so that gate lives at the registration site — mirroring
// the trim-log-database housekeeper. When the log DB is disabled the
// Service is simply never created and no history is recorded (faithful:
// C# returns early, leaving `_prevVersion` null).
type Service struct {
	repo      repo
	version   string
	now       func() time.Time
	logger    *slog.Logger
	publisher Publisher

	mu          sync.Mutex
	prevVersion string
}

// SetPublisher wires the optional UpdateInstalledEvent publisher. Set
// post-construction so NewService's signature stays stable. When unset
// no event is published (the recording still happens). Wire once at
// startup before ApplicationStartedEvent fires.
func (s *Service) SetPublisher(p Publisher) {
	if s == nil {
		return
	}
	s.publisher = p
}

// NewService binds the repo + the running build's version string.
// version is the C# `BuildInfo.Version.ToString()` value (e.g.
// "5.0.0.1234", or "dev" for a developer build). logger defaults to
// slog.Default.
func NewService(r repo, version string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: r, version: version, now: time.Now, logger: logger}
}

// PreviouslyInstalled ports `UpdateHistoryService.PreviouslyInstalled`
// — the version installed before the current one, or "" when there is
// none / on error (C# returns null and logs a warning). Consumed by the
// recent-updates feed's `prevVersion` query param.
func (s *Service) PreviouslyInstalled(ctx context.Context) string {
	h, err := s.repo.PreviouslyInstalled(ctx)
	if err != nil {
		s.logger.Warn("updatehistory: failed to determine previously installed version", slog.Any("err", err))
		return ""
	}
	if h == nil {
		return ""
	}
	return h.Version
}

// InstalledSince ports `UpdateHistoryService.InstalledSince` — the
// Installed rows on/after dateTime; an empty slice on error (C# logs a
// warning and returns an empty list).
func (s *Service) InstalledSince(ctx context.Context, dateTime time.Time) []History {
	out, err := s.repo.InstalledSince(ctx, dateTime)
	if err != nil {
		s.logger.Warn("updatehistory: failed to get list of previously installed versions", slog.Any("err", err))
		return []History{}
	}
	return out
}

// devMajor is the C# dev-branch major version. `BuildInfo.Version.Major
// == 10` is the convention for in-development builds whose version
// "changes constantly", so they are not recorded.
const devMajor = 10

var majorPattern = regexp.MustCompile(`^(\d+)\.`)

// isDevVersion reports whether the running version is a developer build
// that must not be recorded — either the literal "dev" placeholder (no
// parseable major) or a major-10 build. Mirrors the C# guard
// `BuildInfo.Version.Major == 10` plus the Go "dev" default version
// string (which has no numeric major and so is treated as dev).
func isDevVersion(version string) bool {
	m := majorPattern.FindStringSubmatch(version)
	if len(m) < 2 {
		return true
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return true
	}
	return n == devMajor
}

// HandleApplicationStarted ports `UpdateHistoryService.Handle(
// ApplicationStartedEvent)` (`:62-93`). On startup it records the
// running version as an Installed row when it differs from the most
// recent recorded install, and remembers the prior version so a later
// slice can publish `UpdateInstalledEvent`. A corrupted history is
// purged (C# `_repository.Purge()` in the catch). Dev builds are
// skipped.
//
// Subscribed synchronously to lifecycle.ApplicationStartedEvent — the
// recording must complete before any async consumer reads the
// just-stored prevVersion.
func (s *Service) HandleApplicationStarted(ctx context.Context, _ lifecycle.ApplicationStartedEvent) error {
	if isDevVersion(s.version) {
		return nil
	}

	history, err := s.repo.LastInstalled(ctx)
	if err != nil {
		s.logger.Warn("updatehistory: cleaning corrupted update history", slog.Any("err", err))
		if perr := s.repo.Purge(ctx); perr != nil {
			s.logger.Warn("updatehistory: purge failed", slog.Any("err", perr))
		}
		history = nil
	}

	if history != nil && history.Version == s.version {
		return nil
	}

	prev := ""
	if history != nil {
		prev = history.Version
	}
	s.mu.Lock()
	s.prevVersion = prev
	s.mu.Unlock()

	if err := s.repo.Insert(ctx, &History{
		Date:      s.now().UTC(),
		Version:   s.version,
		EventType: EventTypeInstalled,
	}); err != nil {
		s.logger.Warn("updatehistory: failed to record installed version", slog.Any("err", err))
	}

	// Publish UpdateInstalledEvent only on an actual upgrade (prior
	// version recorded), mirroring C#
	// `UpdateHistoryService.HandleAsync(ApplicationStartedEvent)`
	// (`:95-101`) which publishes only when `_prevVersion != null`.
	// Drives the OnApplicationUpdate notification. The consumer
	// subscribes asynchronously so this does not block startup.
	if prev != "" && s.publisher != nil {
		if err := s.publisher(ctx, UpdateInstalledEvent{PreviousVersion: prev, NewVersion: s.version}); err != nil {
			s.logger.Warn("updatehistory: publish UpdateInstalledEvent", slog.Any("err", err))
		}
	}
	return nil
}

// PrevVersion returns the version recorded as previously-installed
// during HandleApplicationStarted (empty until the handler runs, or
// when this is a fresh install with no prior recorded version). A later
// slice reads it to publish UpdateInstalledEvent.
func (s *Service) PrevVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prevVersion
}
