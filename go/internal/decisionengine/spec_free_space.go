package decisionengine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
)

// FreeSpaceSpecificationName is the canonical Spec.Name() identifier.
const FreeSpaceSpecificationName = "FreeSpace"

// FreeSpaceConfigReader narrows the C# IConfigService surface to the
// two getters [FreeSpaceSpecification] reaches for. Production
// binding: [*config.Provider] satisfies this interface via Go's
// structural typing — both getters shipped at slice 2.x / 3.x.
type FreeSpaceConfigReader interface {
	// SkipFreeSpaceCheckWhenImporting → user opt-out for the entire
	// free-space gate. True → spec accepts unconditionally.
	SkipFreeSpaceCheckWhenImporting() bool
	// MinimumFreeSpaceWhenImporting → MB floor below which the spec
	// rejects. Zero would mean "any leftover space is enough" (the
	// post-import remaining-space check still applies).
	MinimumFreeSpaceWhenImporting() int
}

// FreeSpaceDiskReader narrows [disk.Provider.GetAvailableSpace] (one
// method). Production binding: [*disk.Provider] satisfies via Go's
// structural typing.
//
// Returns (bytes, error). The C# `IDiskProvider.GetAvailableSpace`
// returns `long?` and swallows DirectoryNotFoundException at the
// caller; the Go port's GetAvailableSpace returns an error which the
// spec maps to defensive accept (matching the C# behaviour where a
// missing series-path skips the check rather than rejecting).
type FreeSpaceDiskReader interface {
	GetAvailableSpace(path string) (int64, error)
}

// FreeSpaceSpecification rejects releases whose size would exhaust
// the configured free-space floor on the series' disk.
//
// Mirrors the C# class at
// `src/NzbDrone.Core/DecisionEngine/Specifications/FreeSpaceSpecification.cs:1-79`.
//
// # Algorithm
//
//  1. Config.SkipFreeSpaceCheckWhenImporting → accept (user opt-out).
//  2. Resolve free-space at series.Path. Lookup error / DirectoryNotFound
//     → accept defensively (matches C# `catch DirectoryNotFoundException`).
//  3. remainingSpace = freeSpace - release.Size.
//     remainingSpace <= 0 → reject [ReasonMinimumFreeSpace] with
//     "Importing after download will exceed available disk space".
//  4. remainingSpace < MinimumFreeSpaceWhenImporting (MB→bytes) →
//     reject [ReasonMinimumFreeSpace] with the floor breakdown.
//
// Permanent rejection — disk-space issues won't resolve without
// either user action or a disk change which produces a fresh
// Subject. The C# tree marks this Permanent at `:24`.
type FreeSpaceSpecification struct {
	cfg  FreeSpaceConfigReader
	disk FreeSpaceDiskReader
	log  *slog.Logger
}

// NewFreeSpaceSpecification builds the spec. log defaults to
// [slog.Default] when nil. cfg / disk MUST be non-nil.
func NewFreeSpaceSpecification(cfg FreeSpaceConfigReader, disk FreeSpaceDiskReader, log *slog.Logger) (*FreeSpaceSpecification, error) {
	if cfg == nil {
		return nil, fmt.Errorf("decisionengine: FreeSpaceSpecification requires a non-nil FreeSpaceConfigReader")
	}
	if disk == nil {
		return nil, fmt.Errorf("decisionengine: FreeSpaceSpecification requires a non-nil FreeSpaceDiskReader")
	}
	if log == nil {
		log = slog.Default()
	}
	return &FreeSpaceSpecification{cfg: cfg, disk: disk, log: log}, nil
}

// Name returns the canonical spec name.
func (*FreeSpaceSpecification) Name() string { return FreeSpaceSpecificationName }

// Kind reports Permanent.
func (*FreeSpaceSpecification) Kind() RejectionKind { return RejectionPermanent }

// Priority returns Disk — the spec touches the local filesystem.
func (*FreeSpaceSpecification) Priority() SpecPriority { return SpecPriorityDisk }

// IsSatisfiedBy mirrors the C# method at `:30-78`.
func (s *FreeSpaceSpecification) IsSatisfiedBy(_ context.Context, subject *Subject, _ ReleaseDecisionInformation) SpecDecision {
	if subject == nil || subject.Series == nil {
		return Accept()
	}
	rel := releaseFromSubject(subject)
	if rel == nil {
		return Accept()
	}

	if s.cfg.SkipFreeSpaceCheckWhenImporting() {
		s.log.Debug("freeSpace: skip-check enabled; accepting", "title", rel.Title)
		return Accept()
	}

	freeSpace, err := s.disk.GetAvailableSpace(subject.Series.Path)
	if err != nil {
		// Mirror C# `catch DirectoryNotFoundException` arm at `:43-46`.
		// Any disk-lookup failure (missing path, permission denied,
		// etc.) accepts defensively — the import-side free-space
		// check fires later for an actual blocker.
		if errors.Is(err, fs.ErrNotExist) {
			s.log.Debug("freeSpace: series path missing; accepting defensively",
				"path", subject.Series.Path)
		} else {
			s.log.Warn("freeSpace: disk lookup failed; accepting defensively",
				"path", subject.Series.Path, "err", err)
		}
		return Accept()
	}

	size := rel.Size
	remainingSpace := freeSpace - size
	if remainingSpace <= 0 {
		s.log.Debug("freeSpace: post-import would exhaust disk",
			"title", rel.Title, "size", size, "free", freeSpace)
		return Reject(ReasonMinimumFreeSpace,
			"Importing after download will exceed available disk space")
	}

	// MinimumFreeSpaceWhenImporting is in megabytes; convert to bytes
	// for the byte-level compare. 1 MB = 1024*1024 bytes (the C#
	// `Megabytes()` extension at `Common/Extensions/SizeExtensions.cs`).
	minimumSpaceMB := int64(s.cfg.MinimumFreeSpaceWhenImporting())
	minimumSpace := minimumSpaceMB * 1024 * 1024
	if remainingSpace < minimumSpace {
		s.log.Debug("freeSpace: remaining below minimum",
			"title", rel.Title, "remaining", remainingSpace, "minimum", minimumSpace)
		return Reject(ReasonMinimumFreeSpace,
			fmt.Sprintf("Not enough free space (%s) to import after download: %s. (Settings: Media Management: Minimum Free Space)",
				formatBytesSI(minimumSpace), formatBytesSI(remainingSpace)))
	}

	return Accept()
}

// formatBytesSI is a small SI-suffix formatter that matches the C#
// `SizeExtensions.SizeSuffix` output shape (e.g. "100.0 MB", "1.5 GB").
// Inlined here because the C# helper is part of NzbDrone.Common which
// the Go port hasn't broken out into a shared formatter yet — every
// rejection-message-in-bytes spec inlines its own suffix logic until
// a host-side formatter ports.
func formatBytesSI(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KB", "MB", "GB", "TB", "PB", "EB"}[exp]
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), suffix)
}

// Compile-time interface satisfaction.
var _ Specification = (*FreeSpaceSpecification)(nil)
