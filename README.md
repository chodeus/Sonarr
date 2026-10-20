# Sonarr custom build

`ghcr.io/chodeus/sonarr:custom` is Sonarr's `develop` branch (v5) with the changes below merged in, on the hotio base image.

## What it adds to Sonarr develop

- **Title matching for event series** — branch [`feat/title-match`](https://github.com/chodeus/Sonarr/tree/feat/title-match), not submitted
  - For series tagged `title-match`, a release named by event instead of episode number, such as `Formula1.2026.Singapore.Grand.Prix` or `Formula1.2026.Round17.Singapore.Race`, is matched to the episode whose title names the same place and session.
  - The year picks the season. Round numbers (`Round17`, `R17`, `2026x17`) pick the weekend and must agree with the place. A name with no session word is the race only if it says "Grand Prix" or "GP".
  - A release that names no event maps to no episode instead of the parser's numbers. Explicit `S2026E94` numbering, Sonarr's own file names and specials found by their title keep their episode.
  - Shows, press conferences, testing and sibling series are never matched, and anything ambiguous is left unmatched.
  - A nationality that names the event, such as "Italian" in "Italian Grand Prix", no longer counts as the audio language of the release or its imported file. A language word on its own, such as ITALIAN on another event, is the audio language.
  - Applies to grabs, searches, imports, and pairing subtitles and extras. Series without the tag are unchanged.
- **v3 release profiles on any indexer** — branch [`fix/v3-release-profile-any-indexer`](https://github.com/chodeus/Sonarr/tree/fix/v3-release-profile-any-indexer), not submitted
  - A v3 API save with `indexerId: 0` means any indexer again. Stock v5 stores it as `[0]`, which matches no indexer, so the profile's "Must not contain" terms and air-date restriction silently stop applying.
  - A v3 save that keeps the profile's first indexer keeps its full indexer list, and a profile already stored as `[0]` is repaired on its next v3 save with `indexerId: 0`.
- **Blocked season packs import once their titles arrive** — branch [`fix/same-release-regrab`](https://github.com/chodeus/Sonarr/tree/fix/same-release-regrab), which also carries the next item, not submitted
  - A queued download whose torrent name doesn't match the series, such as `The.Diplomat.S04…` for "The Diplomat (US)", keeps the series it was grabbed for when the series is edited. It used to lose it, stay in Importing for good, and let the season be grabbed again from another release.
  - When a refresh removes one of a waiting pack's episodes (TheTVDB dropping a TBA placeholder), the pack keeps its series and the other episodes. It used to lose the series until a restart.
  - A remap that fails keeps the download's series and the reason its import is blocked. A download whose series was deleted is left with no series instead of pointing at it.
  - A download with no episodes mapped is no longer counted as already imported.
  - Episode Title Required "Only for Bulk Season Releases" checks the episode's own season, not the season numbered like the episode.
- **The same release isn't grabbed twice** — branch [`fix/same-release-regrab`](https://github.com/chodeus/Sonarr/tree/fix/same-release-regrab), not submitted
  - A queued download is scored from its torrent name. When that name lacks tokens the grabbed title had, such as Atmos or HDR10+, the same release from another feed used to pass as an upgrade and be grabbed again while the first was still downloading.
  - A release is now refused while a download with the same torrent hash, or the same title as its torrent name or grabbed title, is in the queue. Case, separators and a file extension are ignored. Scores are unchanged.
  - New qBittorrent setting "Rename to Release Title", off by default: until a torrent imports, it's renamed to its grabbed title when that title scores higher, so the queue scores what was grabbed. Files inside aren't renamed. With the setting off, no rename checks run at all.
- **Episode order changes after import** — branch [`feat/episode-order-change`](https://github.com/chodeus/Sonarr/tree/feat/episode-order-change), not submitted
  - Edit Series can change Episode Order (Aired, DVD, International, …) on a series that already has files. Stock v5 locks it once a file exists.
  - Choosing another order shows a preview first: how many files need a new name or folder, Show Changes with each file's existing and new path, and anything that blocks the change. Saving applies the order and, with Rename and Move Files (on by default), renames the files.
  - The change is refused while any file belongs to an episode missing from the new order, a combined file couldn't be named for it, or episodes still lack TVDB IDs (refresh the series once after updating). Leaving Aired order is also refused while XEM or a scene title numbers the series' releases, since the other orders turn both off.
  - If the refresh after the change doesn't put every file where the preview said, the previous order is put back and no file is renamed.
  - A refresh follows an episode TVDB renumbers by its TVDB ID, so its file stays with it.
- **A torrent qBittorrent already has isn't retried every sync** — branch [`fix/qbit-duplicate-reject`](https://github.com/chodeus/Sonarr/tree/fix/qbit-duplicate-reject), not submitted
  - When qBittorrent refuses a torrent it already holds, such as one in a post-import category or one a cross-seed tool added, the release used to be grabbed and refused again on every RSS sync. It is now rejected with "qBittorrent already has this release".
  - Older qBittorrent versions answer with "Fails." instead of a conflict. That reply gets the same check: it's treated as a duplicate only when qBittorrent has the torrent, and any other failed add is reported as before.
  - Only the same torrent hash, or the same listing on the same indexer, is rejected; another release with the same title isn't.
  - Nothing is blocklisted. The rejection lives in memory for 12 hours, and each sync first asks qBittorrent whether it still has the torrent, so deleting it there lets the next sync grab the release. A refusal for the same release that arrives while that check runs is kept.
- **A series refresh keeps changes saved while it runs** — branch [`fix/refresh-keeps-concurrent-changes`](https://github.com/chodeus/Sonarr/tree/fix/refresh-keeps-concurrent-changes), not submitted
  - A refresh used to save the whole series after its TheTVDB lookup, undoing anything saved meanwhile. A folder move got its old path and pending-move mark back (the series then looked mid-move until a restart), a season toggled during the lookup was switched back along with its episodes, and a profile change was lost.
  - It now reads the series again after the lookup and saves only the fields it refreshes. If the ID, language or episode order changed during the lookup, it looks the series up again.
  - Auto-tags are saved alone, keeping a tag edit made meanwhile. The rescan after a refresh reads the series again, so a move that finished is scanned at its new path: a scan of a folder that's gone drops every episode-file record.
  - The episode refresh that follows saves only the episode columns it refreshes, so a monitor change, an import linking a file or a search time saved meanwhile stays.
- **Delay profile saves, reorders and deletes don't undo each other** — branch [`fix/delay-profile-order`](https://github.com/chodeus/Sonarr/tree/fix/delay-profile-order), not submitted
  - A reorder used to put back any profile saved while it ran. A save wrote the order its edit form had loaded, so saving after a reorder made elsewhere moved the profile back, and two profiles could share an order.
  - Reorder and delete now save only the order, and a save keeps the stored order. A new profile goes after the highest order, and the default profile stays last.
  - The 30-second best-profile cache is cleared after every change, including a reorder and a delete whose renumber fails, so releases aren't held by an old ranking or a deleted profile. The settings page reloads the profiles after a delete.
  - The best-profile lookup, and the queue's estimated time for a held release, rank the default profile last even when an older save stored it out of place or a tagged profile shares its order.

## How the build works

- Each change is its own branch off `develop` in [chodeus/Sonarr](https://github.com/chodeus/Sonarr).
- `meta.json` lists them in `pr_branches`, each at the commit in `pr_shas`. The `update` workflow, every 3 hours, follows upstream `develop` and the base image, and a change to either triggers a build. It never moves `pr_shas`, so a branch can be pushed for review without reaching the image.
- `build.sh source` merges the branches into `develop` in the listed order. If one no longer merges, the build fails and names it; rebase that branch onto `develop`.
- `build.sh compile` follows upstream's own CI build. NuGet advisories are logged rather than fatal, because upstream suppresses them one at a time and a new one would otherwise stop every image update.
- Once a branch's review is clean, `./build.sh pin <branch>` records its head in `pr_shas` (adding the branch if it's new) and the push builds the image. To drop a change, remove it from both `pr_branches` and `pr_shas`.
- Build-only migrations are numbered from 900 so upstream's own numbers can't collide.
