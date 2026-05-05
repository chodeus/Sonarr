// Package decisionengine is the Go port of NzbDrone.Core.DecisionEngine —
// the per-release scoring + rejection pipeline that decides whether a
// candidate release from an indexer (or a manual push) should be
// downloaded.
//
// # What this slice (Phase 3.30) ships
//
//   - The decision data model: Result/Rejection enums, the SpecDecision
//     factory pair (Accept/Reject), Rejection value type, Decision
//     struct with derived predicates (Approved / TemporarilyRejected /
//     Rejected). This is the LOAD-BEARING shape every future
//     Specification port writes against.
//   - ReleaseSourceType enum — the C# `ReleaseSourceType` at
//     `Parser/Model/RemoteEpisode.cs:47-55`. Lives next to the
//     decision model rather than in `internal/parser` because the
//     decision pipeline is the sole consumer (the parser doesn't
//     branch on it).
//
// # What's deliberately deferred
//
// In rough sequencing order:
//
//   - **Subject (RemoteEpisode equivalent)** — the cross-domain bag
//     a Specification reads. Needs `parser.ReleaseInfo`,
//     `parser.ParsedEpisodeInfo`, scene-mapping refs, indexer + DC
//     references. Its own slice once those surfaces land in the Go
//     port. Decision.Subject below uses `any` as a placeholder; once
//     the real Subject type lands, that field types over.
//   - **Specification interface** — `IDownloadDecisionEngineSpecification`
//     port. Trivial to add but pointless without a real Subject; ports
//     in the same slice as Subject.
//   - **UpgradableSpecification + the ~30 concrete Specifications**
//     each lands in its own slice once Subject is there. Quality /
//     custom-format math (`quality.Compare`, `qualityprofiles.GetIndex`,
//     `qualityprofiles.CalculateCustomFormatScore`) is already shipped
//     so most specs are direct ports.
//   - **DownloadDecisionMaker orchestrator** — runs every Specification
//     against a list of candidate releases, fans out into a Decision
//     list. Lands after the spec interface + a critical mass of
//     concrete specs.
//   - **DownloadDecisionComparer** — the upgrade-vs-current-disk-state
//     comparator that drives the Prioritization service. Largely a
//     reordering pass on top of the decisionengine surface; ports
//     after the Maker.
//
// # Naming alignment
//
// The C# tree uses `DownloadDecision` / `DownloadRejection` /
// `DownloadRejectionReason` / `DownloadSpecDecision`. The Go port drops
// the `Download` prefix — this package's purpose is exactly the
// download-vs-not decision; the prefix would be redundant noise on
// every reference. Equivalents:
//
//	C# `DownloadDecision`        → `decisionengine.Decision`
//	C# `DownloadRejection`       → `decisionengine.Rejection`
//	C# `DownloadRejectionReason` → `decisionengine.RejectionReason`
//	C# `DownloadSpecDecision`    → `decisionengine.SpecDecision`
//	C# `RejectionType`           → `decisionengine.RejectionKind`
//	C# `UpgradeableRejectReason` → `decisionengine.UpgradeableRejectReason`
//	C# `SpecificationPriority`   → `decisionengine.SpecPriority`
//
// `RejectionType` collides with Go's standard reflect.Type usage
// pattern when both are imported, so `RejectionKind` is the chosen
// rename. The new name carries the same Permanent/Temporary semantics.
package decisionengine
