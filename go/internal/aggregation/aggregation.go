// Package aggregation ports
// `src/NzbDrone.Core/Download/Aggregation/RemoteEpisodeAggregationService.cs:1-48`
// — the post-Map enrichment pass that decorates a decisionengine.Subject
// (the Go-port equivalent of RemoteEpisode) with cross-source fields the
// raw parser output doesn't carry: indexer-driven Languages refinement,
// future ReleaseType detection, future IndexerFlags, etc.
//
// Slice 4.T (sub-arc 11b of queue [11]). Lands:
//
//   - The orchestrating [Service] + the [Augmenter] narrow interface.
//   - One concrete augmenter: [AggregateLanguages] (the only augmenter
//     present at HEAD; AggregateLanguagesFixture.cs covers the test
//     surface).
//
// The downstream consumer is [decisionengine.DownloadDecisionMaker]
// (sub-arc 11c — slice 4.U or follow-on). The Augment call sits
// BETWEEN parsingservice.Service.Map (parsed → Subject) and the
// DecisionMaker.Decide spec evaluation:
//
//	subject := parsingsvc.Map(...)
//	aggregation.Augment(subject)
//	decision := decisionmaker.Decide(subject, info)
//
// Per-augmenter contract: augmenters MUST tolerate a partially-
// populated Subject (Series may be nil, Release may be nil,
// ParsedEpisodeInfo is guaranteed non-nil). They MUST NOT replace the
// Subject reference; they mutate sub-fields in place.
package aggregation

import (
	"context"
	"errors"
	"log/slog"

	"sonarr.tv/sonarr/internal/decisionengine"
)

// Augmenter is the narrow interface concrete enrichment passes
// implement. Mirrors C# `IAggregateRemoteEpisode` at
// `src/NzbDrone.Core/Download/Aggregation/Aggregators/IAggregateRemoteEpisode.cs:1-9`.
//
// The C# return type is RemoteEpisode (the same instance, by C#
// reference-semantics); Go uses an error return + in-place mutation
// of *decisionengine.Subject. The C# `IAggregateRemoteEpisode.Aggregate`
// has no error channel — exceptions propagate to the catch-all in
// `RemoteEpisodeAggregationService.Augment` (`:33-43`). The Go port
// returns error so the orchestrator can log+continue without panic
// recovery; the contract is the same ("swallow per-augmenter failure;
// other augmenters still run").
type Augmenter interface {
	// Aggregate mutates sub in place to apply this augmenter's
	// enrichment. Returns nil on success; any non-nil error is logged
	// by the orchestrator and the next augmenter still runs.
	//
	// Augmenters MUST NOT swap sub for a different pointer — the
	// caller holds the reference. Augmenters MAY swap any field on
	// sub.
	Aggregate(ctx context.Context, sub *decisionengine.Subject) error
}

// Service is the orchestrator that calls every registered [Augmenter]
// in order. Mirrors C# `RemoteEpisodeAggregationService` at
// `src/NzbDrone.Core/Download/Aggregation/RemoteEpisodeAggregationService.cs:14-47`.
//
// Goroutine-safe post-construction: the augmenters slice is fixed at
// NewService time. Individual augmenters MUST be goroutine-safe if
// the Service is shared across Decide goroutines (the production
// augmenters at HEAD are stateless).
type Service struct {
	augmenters []Augmenter
	log        *slog.Logger
}

// NewService binds the augmenter list + logger. augmenters MAY be
// empty (the resulting Service.Augment is a no-op pass-through).
// Returns error rather than nil-Service so callers can distinguish
// wiring misuse from "no augmenters at HEAD" at startup.
//
// log == nil → slog.Default. Matches the pattern from sibling service
// constructors (parsingservice.NewService, decisionengine.NewService).
func NewService(augmenters []Augmenter, log *slog.Logger) (*Service, error) {
	for i, a := range augmenters {
		if a == nil {
			return nil, errors.New("aggregation: NewService: nil augmenter at index " + itoa(i))
		}
	}
	if log == nil {
		log = slog.Default()
	}
	// Defensive copy — the caller can't mutate the slice after
	// construction. Matches the slice-defensive-copy convention from
	// decisionengine.NewDecisionMaker.
	cp := make([]Augmenter, len(augmenters))
	copy(cp, augmenters)
	return &Service{augmenters: cp, log: log}, nil
}

// Augment runs every registered augmenter against sub in registration
// order. Returns sub for ergonomic chaining (mirrors C# `:26-46`
// which returns RemoteEpisode for the same reason); the same pointer
// is returned whether or not any augmenter erred. nil sub returns
// nil + no error — mirrors C# `:28-31` early-return.
//
// Per-augmenter errors are logged at Warn (mirrors C# `_logger.Warn(ex,
// ex.Message)` at `:41`) but DO NOT halt the pipeline. The C# version
// catches Exception, swallows, and continues; the Go port does the
// same with the augmenter's returned error.
func (s *Service) Augment(ctx context.Context, sub *decisionengine.Subject) *decisionengine.Subject {
	if s == nil || sub == nil {
		return sub
	}
	for _, a := range s.augmenters {
		if err := a.Aggregate(ctx, sub); err != nil {
			s.log.Warn("aggregation: augmenter failed",
				"augmenter", augmenterName(a),
				"err", err)
		}
	}
	return sub
}

// augmenterName returns a concise type identifier for log lines.
// Mirrors the C# log line at `:41` which surfaces the augmenter
// concrete type via the exception trace; the Go port can't get the
// concrete type from the interface without reflection so we use the
// Stringer convention when the augmenter implements it, else a
// default placeholder.
func augmenterName(a Augmenter) string {
	if s, ok := a.(interface{ String() string }); ok {
		return s.String()
	}
	return "<unnamed>"
}

// itoa is a deliberately tiny int→string helper used by NewService's
// error message construction (avoids dragging strconv into the
// package for this one path).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
