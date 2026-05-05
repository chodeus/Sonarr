package decisionengine

// RejectionReason ports the C# `DownloadRejectionReason` enum at
// `src/NzbDrone.Core/DecisionEngine/DownloadRejectionReason.cs:1-77`.
// 75 entries; each Specification port returns one of these on rejection
// so downstream UI / logs can render a localised message + (eventually)
// route the user to a remediation flow.
//
// Order MUST match the C# declaration exactly — the integer value flows
// through the v3 API envelope and is rendered by the React frontend
// via a per-int lookup table. Re-ordering shifts every wire value.
// New entries MUST append to the end (mirrors the C# practice).
//
// Naming alignment: every entry is prefixed `Reason` to disambiguate
// from spec-name constants in future slices (e.g. `MaximumSizeExceeded`
// the rejection reason vs `MaximumSizeSpecification` the spec).
type RejectionReason int

// RejectionReason enumeration. The 75-entry list mirrors the C#
// declaration verbatim. Comments tag the consuming Specification —
// useful when porting that spec, since the rejection-reason name is
// its primary identifier.
const (
	// ReasonUnknown is the zero default. Should never appear on a
	// shipped Rejection — checks always return a more specific reason.
	ReasonUnknown RejectionReason = iota
	// ReasonUnknownSeries — the parsed release didn't match any known
	// series. Returned by the early parser-side rejection in
	// DownloadDecisionMaker before any Specification runs.
	ReasonUnknownSeries
	// ReasonUnknownEpisode — series matched but the episode number
	// doesn't correspond to a known episode row.
	ReasonUnknownEpisode
	// ReasonMatchesAnotherSeries — series-title-info matches multiple
	// series and we can't disambiguate.
	ReasonMatchesAnotherSeries
	// ReasonUnableToParse — release title couldn't be parsed at all.
	ReasonUnableToParse
	// ReasonError — generic spec-internal error; surfaces the exception
	// message in the UI.
	ReasonError
	// ReasonDecisionError — orchestrator-side error (the
	// DownloadDecisionMaker swallowed an exception from a spec).
	ReasonDecisionError
	// ReasonMinimumAgeDelay — DelayProfile says wait N minutes after
	// release before grabbing.
	ReasonMinimumAgeDelay
	// ReasonMinimumAgeDelayPushed — same as MinimumAgeDelay but the
	// release was a manual push, so the delay should be skipped (the
	// rejection records this for history rather than blocking).
	ReasonMinimumAgeDelayPushed
	// ReasonSeriesNotMonitored — Series.Monitored == false.
	ReasonSeriesNotMonitored
	// ReasonEpisodeNotMonitored — Episode.Monitored == false on at
	// least one of the requested episodes.
	ReasonEpisodeNotMonitored
	// ReasonHistoryRecentCutoffMet — HistorySpecification: a recent
	// grab in History already meets the quality cutoff.
	ReasonHistoryRecentCutoffMet
	// ReasonHistoryCdhDisabledCutoffMet — HistorySpecification with
	// completed-download-handling disabled: cutoff met without a CDH
	// entry.
	ReasonHistoryCdhDisabledCutoffMet
	// ReasonHistoryHigherPreference — HistorySpecification: a recent
	// grab is a higher-preference quality (within the same item).
	ReasonHistoryHigherPreference
	// ReasonHistoryHigherRevision — HistorySpecification: a recent
	// grab is a same-quality higher-revision (proper / repack).
	ReasonHistoryHigherRevision
	// ReasonHistoryCutoffMet — HistorySpecification: cutoff met by an
	// older history row.
	ReasonHistoryCutoffMet
	// ReasonHistoryCustomFormatCutoffMet — HistorySpecification:
	// custom-format cutoff met by a history row.
	ReasonHistoryCustomFormatCutoffMet
	// ReasonHistoryCustomFormatScore — HistorySpecification: history
	// row has a higher custom-format score than the candidate.
	ReasonHistoryCustomFormatScore
	// ReasonHistoryCustomFormatScoreIncrement — HistorySpecification:
	// score doesn't beat history by the profile's minimum increment.
	ReasonHistoryCustomFormatScoreIncrement
	// ReasonHistoryUpgradesNotAllowed — HistorySpecification:
	// QualityProfile.UpgradeAllowed == false.
	ReasonHistoryUpgradesNotAllowed
	// ReasonNoMatchingTag — series has tag predicates that the release
	// doesn't satisfy (release-profile tags).
	ReasonNoMatchingTag
	// ReasonPropersDisabled — proper / repack release rejected because
	// `DownloadPropersAndRepacks == DoNotPrefer`.
	ReasonPropersDisabled
	// ReasonProperForOldFile — proper for a file older than
	// EnableCompletedDownloadHandlingMaxDays.
	ReasonProperForOldFile
	// ReasonWrongEpisode — episode number(s) don't match the request
	// (interactive-search shape).
	ReasonWrongEpisode
	// ReasonWrongSeason — season doesn't match the request.
	ReasonWrongSeason
	// ReasonWrongSeries — series doesn't match the request.
	ReasonWrongSeries
	// ReasonFullSeason — full-season pack when the request was a
	// single episode (FullSeasonSpecification).
	ReasonFullSeason
	// ReasonUnknownRuntime — episode runtime not yet available;
	// AcceptableSizeSpecification can't reason about it.
	ReasonUnknownRuntime
	// ReasonBelowMinimumSize — release size is below the
	// QualityDefinitions minimum-size floor.
	ReasonBelowMinimumSize
	// ReasonAboveMaximumSize — release size is above the
	// QualityDefinitions maximum-size ceiling.
	ReasonAboveMaximumSize
	// ReasonAlreadyImportedSameHash — exact same hash already in
	// History (AlreadyImportedSpecification).
	ReasonAlreadyImportedSameHash
	// ReasonAlreadyImportedSameName — same release name (different
	// hash) already in History.
	ReasonAlreadyImportedSameName
	// ReasonUnknownReleaseGroup — release-profile requires a known
	// release group; this release has none.
	ReasonUnknownReleaseGroup
	// ReasonReleaseGroupDoesNotMatch — release-profile constrains to
	// specific release groups; this isn't one of them.
	ReasonReleaseGroupDoesNotMatch
	// ReasonIndexerDisabled — indexer that produced the release is
	// disabled (BlockedIndexerSpecification).
	ReasonIndexerDisabled
	// ReasonBlocklisted — release is on the blocklist
	// (BlocklistSpecification).
	ReasonBlocklisted
	// ReasonCustomFormatMinimumScore — release's custom-format score
	// is below the quality profile's MinFormatScore floor.
	ReasonCustomFormatMinimumScore
	// ReasonMinimumFreeSpace — disk free space below the configured
	// minimum (FreeSpaceSpecification).
	ReasonMinimumFreeSpace
	// ReasonFullSeasonNotAired — full-season pack but not all
	// episodes have aired (SeasonPackOnlySpecification).
	ReasonFullSeasonNotAired
	// ReasonMaximumSizeExceeded — config-level "never-larger-than"
	// ceiling, distinct from QualityDefinitions max
	// (MaximumSizeSpecification).
	ReasonMaximumSizeExceeded
	// ReasonMinimumAge — global minimum-age floor (different from
	// the DelayProfile MinimumAgeDelay above).
	ReasonMinimumAge
	// ReasonMaximumAge — release older than the retention floor.
	ReasonMaximumAge
	// ReasonMultiSeason — multi-season pack when the request is one
	// season (MultiSeasonSpecification).
	ReasonMultiSeason
	// ReasonSample — release classified as a sample
	// (NotSampleSpecification — but the reason is `Sample` not
	// `NotSample`).
	ReasonSample
	// ReasonProtocolDisabled — release uses a download protocol
	// (Usenet / Torrent) that's globally disabled.
	ReasonProtocolDisabled
	// ReasonQualityNotWanted — release's parsed quality isn't in the
	// quality profile's allowed set.
	ReasonQualityNotWanted
	// ReasonQualityUpgradesDisabled — release would be an upgrade but
	// the profile's UpgradeAllowed is false.
	ReasonQualityUpgradesDisabled
	// ReasonQueueHigherPreference — QueueSpecification: the queue
	// already has a higher-preference grab in flight.
	ReasonQueueHigherPreference
	// ReasonQueueHigherRevision — QueueSpecification: queue has a
	// same-quality higher-revision in flight.
	ReasonQueueHigherRevision
	// ReasonQueueCutoffMet — QueueSpecification: in-flight grab
	// already meets the quality cutoff.
	ReasonQueueCutoffMet
	// ReasonQueueCustomFormatCutoffMet — QueueSpecification:
	// in-flight grab meets the custom-format cutoff score.
	ReasonQueueCustomFormatCutoffMet
	// ReasonQueueCustomFormatScore — QueueSpecification: in-flight
	// grab has a higher custom-format score than the candidate.
	ReasonQueueCustomFormatScore
	// ReasonQueueCustomFormatScoreIncrement — QueueSpecification:
	// candidate's score doesn't beat in-flight by minimum increment.
	ReasonQueueCustomFormatScoreIncrement
	// ReasonQueueUpgradesNotAllowed — QueueSpecification:
	// QualityProfile.UpgradeAllowed == false.
	ReasonQueueUpgradesNotAllowed
	// ReasonQueuePropersDisabled — QueueSpecification: release is a
	// proper but DownloadPropersAndRepacks == DoNotPrefer.
	ReasonQueuePropersDisabled
	// ReasonRaw — RawDiskSpecification: release is a raw-disk format
	// (BD ISO / DVD VOB) and the user has raw-disks disabled.
	ReasonRaw
	// ReasonMustContainMissing — ReleaseProfile required-tokens not
	// satisfied.
	ReasonMustContainMissing
	// ReasonMustNotContainPresent — ReleaseProfile forbidden-tokens
	// found.
	ReasonMustNotContainPresent
	// ReasonRepackDisabled — RepackSpecification: repack release but
	// DownloadPropersAndRepacks == DoNotPrefer.
	ReasonRepackDisabled
	// ReasonRepackUnknownReleaseGroup — RepackSpecification: repack
	// without a release group can't be matched against the previous
	// grab's group.
	ReasonRepackUnknownReleaseGroup
	// ReasonRepackReleaseGroupDoesNotMatch — RepackSpecification: repack
	// is from a different release group than the previous grab.
	ReasonRepackReleaseGroupDoesNotMatch
	// ReasonExistingFileHasMoreEpisodes — SameEpisodesSpecification:
	// existing on-disk file covers more episodes than the candidate.
	ReasonExistingFileHasMoreEpisodes
	// ReasonAmbiguousNumbering — anime numbering is ambiguous and the
	// matcher can't pick (AnimeVersionUpgradeSpecification etc).
	ReasonAmbiguousNumbering
	// ReasonNotSeasonPack — SeasonPackOnlySpecification: requested a
	// season pack but the release isn't one.
	ReasonNotSeasonPack
	// ReasonSplitEpisode — split-episode release (E01.5/E02 style)
	// rejected by SplitEpisodeSpecification.
	ReasonSplitEpisode
	// ReasonMinimumSeeders — TorrentSeedingSpecification: seeder
	// count below the user's minimum.
	ReasonMinimumSeeders
	// ReasonDiskHigherPreference — UpgradeDiskSpecification: existing
	// file is already a higher-preference quality.
	ReasonDiskHigherPreference
	// ReasonDiskHigherRevision — UpgradeDiskSpecification: existing
	// file is same-quality higher-revision.
	ReasonDiskHigherRevision
	// ReasonDiskCutoffMet — UpgradeDiskSpecification: existing file
	// meets the quality cutoff.
	ReasonDiskCutoffMet
	// ReasonDiskCustomFormatCutoffMet — UpgradeDiskSpecification:
	// existing file meets the custom-format cutoff score.
	ReasonDiskCustomFormatCutoffMet
	// ReasonDiskCustomFormatScore — UpgradeDiskSpecification: existing
	// file has a higher score than the candidate.
	ReasonDiskCustomFormatScore
	// ReasonDiskCustomFormatScoreIncrement — UpgradeDiskSpecification:
	// candidate doesn't beat existing by the minimum increment.
	ReasonDiskCustomFormatScoreIncrement
	// ReasonDiskUpgradesNotAllowed — UpgradeDiskSpecification: profile
	// UpgradeAllowed == false.
	ReasonDiskUpgradesNotAllowed
	// ReasonDiskNotUpgrade — UpgradeDiskSpecification: catch-all "not
	// an upgrade" path beyond the specific reasons above.
	ReasonDiskNotUpgrade
	// ReasonBeforeAirDate — AcceptableSizeSpecification (and others):
	// release uploaded before the episode's air date — unlikely to
	// be a real grab.
	ReasonBeforeAirDate
)
