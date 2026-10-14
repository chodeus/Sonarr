# Fixed: Keep v3 release profiles on any indexer

Part of the [custom Sonarr image](https://github.com/chodeus/Sonarr/tree/custom) (`ghcr.io/chodeus/sonarr:custom`), which merges this branch into `develop`. Reviewed in [#2](https://github.com/chodeus/Sonarr/pull/2).

v5 stores a release profile's indexers as a list, `IndexerIds`, where an empty list means any indexer. The v3 API still has a single `indexerId`, where 0 means any indexer. Stock v5 turns a v3 save with `indexerId: 0` into `IndexerIds = [0]`. No indexer has id 0, so `ReleaseProfileService.EnabledForTags` drops the profile for every release, and its "Must not contain" terms and air-date restriction silently stop applying. Any v3 client that saves a release profile can trigger it.

## Changes

- `ReleaseProfileResource.ToModel` maps `indexerId: 0` to an empty list, and any other id to a list of just that indexer.
- v3 shows only the first of a profile's indexers. When a v3 client saves a profile back with that first indexer unchanged, `ReleaseProfileController.Update` keeps the full list. A save with `indexerId: 0` always clears it, so a profile already stored as `[0]` is repaired on its next save.

## Tests

- `ReleaseProfilesFixture`: `indexerId` 0 maps to any indexer, and 5 maps to `[5]`.
- `ReleaseProfileControllerFixture`: an unchanged first indexer keeps `[3, 5]`, a new indexer replaces the list, and 0 clears it, including a stored `[0]`.
- Each change was undone on purpose to confirm its tests fail. All 40 tests in `Sonarr.Api.Test` pass.

## Summary by CodeRabbit

* **Bug Fixes**
  * Updating a release profile without changing its primary indexer now preserves all associated indexers.
  * Setting the indexer to "any" now correctly clears specific indexer selections.
