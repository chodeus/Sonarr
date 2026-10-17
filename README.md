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
- **Blocked season packs import once their titles arrive** — branch [`fix/blocked-import-retry`](https://github.com/chodeus/Sonarr/tree/fix/blocked-import-retry), not submitted
  - A queued download whose torrent name doesn't match the series, such as `The.Diplomat.S04…` for "The Diplomat (US)", keeps the series it was grabbed for when the series is edited. It used to lose it, stay in Importing for good, and let the season be grabbed again from another release.
  - A download with no episodes mapped is no longer counted as already imported.
  - Episode Title Required "Only for Bulk Season Releases" checks the episode's own season, not the season numbered like the episode.
- **The same release isn't grabbed twice** — branch [`fix/same-release-regrab`](https://github.com/chodeus/Sonarr/tree/fix/same-release-regrab), not submitted
  - A queued download is scored from its torrent name. When that name lacks tokens the grabbed title had, such as Atmos or HDR10+, the same release from another feed used to pass as an upgrade and be grabbed again while the first was still downloading.
  - A release is now refused while a download with the same torrent hash, or the same title as its torrent name or grabbed title, is in the queue. Case, separators and a file extension are ignored. Scores are unchanged.
  - New qBittorrent setting "Rename to Release Title", off by default: a downloading torrent is renamed to its grabbed title when that title scores higher, so the queue scores what was grabbed. Files inside aren't renamed.
- **Episode order changes after import** — branch [`feat/episode-order-change`](https://github.com/chodeus/Sonarr/tree/feat/episode-order-change), not submitted
  - Edit Series can change Episode Order (Aired, DVD, International, …) on a series that already has files. Stock v5 locks it once a file exists.
  - Choosing another order shows a preview first: how many files need a new name or folder, Show Changes with each file's existing and new path, and anything that blocks the change. Saving applies the order and, with Rename and Move Files (on by default), renames the files.
  - The change is refused while any file belongs to an episode missing from the new order, a combined file couldn't be named for it, or episodes still lack TVDB IDs (refresh the series once after updating). Leaving Aired order is also refused while XEM or a scene title numbers the series' releases, since the other orders turn both off.
  - If the refresh after the change doesn't put every file where the preview said, the previous order is put back and no file is renamed.
  - Episodes now keep their TVDB IDs (stock v5 stores 0 for every episode), and a refresh follows an episode TVDB renumbers by ID, so its file stays with it.

## How the build works

- Each change is its own branch off `develop` in [chodeus/Sonarr](https://github.com/chodeus/Sonarr).
- `meta.json` lists them in `pr_branches`, each at the commit in `pr_shas`. The `update` workflow, every 3 hours, follows upstream `develop` and the base image, and a change to either triggers a build. It never moves `pr_shas`, so a branch can be pushed for review without reaching the image.
- `build.sh source` merges the branches into `develop` in the listed order. If one no longer merges, the build fails and names it; rebase that branch onto `develop`.
- `build.sh compile` follows upstream's own CI build. NuGet advisories are logged rather than fatal, because upstream suppresses them one at a time and a new one would otherwise stop every image update.
- Once a branch's review is clean, `./build.sh pin <branch>` records its head in `pr_shas` (adding the branch if it's new) and the push builds the image. To drop a change, remove it from both `pr_branches` and `pr_shas`.
- Build-only migrations are numbered from 900 so upstream's own numbers can't collide.
