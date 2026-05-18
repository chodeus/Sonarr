# Phase 5 Find-and-Fix Autonomous Loop Prompt

Paste the block below into `/loop <<autonomous-loop-dynamic>>` (or use as the seed prompt for `ScheduleWakeup`) to resume the fix-mission. Each iteration ships ONE tight slice from `go/PHASE_5_AUDIT.md`.

---

## Loop seed prompt

```
Resume the Go-port autonomous fix loop on the go-port branch of
/Users/dean/Documents/GitHub/Sonarr.

State at pause (verify with `git -C /Users/dean/Documents/GitHub/Sonarr log --oneline | head`):

HEAD: e043c3110 — Phase 4.FW (Phase 5 byte-equal closure FINAL): notification.schema
Schema-endpoint arc closed (4.FT–4.FW): importlist + indexer + downloadclient +
notification all byte-equal against C# goldens. Phase 5 byte-equal count 45/51.

Four-axis audit at PHASE_5_AUDIT.md surfaced the real gap inventory: zero write
endpoints, 25/75 providers schema-only (incl. qBittorrent), error-envelope drift,
zero handler-side logging. The audit doc IS the canonical backlog for this loop.

Refresh state with `cat go/PHASE_5_AUDIT.md` (gap inventory + tiered backlog) and
`awk 'NR==9 {print substr($0, length($0)-2500); exit}' go/STATUS.md` (rolling
slice log tail) before picking.

Backlog ordering (largest chunk first; consume top-down):

  Tier 1 — Write paths (multi-week)
    5.A1–A5  Foundational infra (httperr / validation / signalr-emitter /
             error-sweep / logging-thread).  ← MUST land first; unblocks every
             write slice and improves every existing handler.
    5.B1–B13 Simple resource CRUD (tag → rootfolder → customfilter → customformat
             → qualityprofile → releaseprofile → qualitydefinition → delayprofile
             → remotepathmapping → autotagging → importlistexclusion →
             namingconfig → config/* PUTs).
    5.C1–C5  Provider CRUD (indexer / importlist / downloadclient / notification
             / metadata, sharing a ProviderControllerBase-style adapter).
    5.D1–D6  Series/episode/episodefile/seasonpass.
    5.E1–E4  Command/queue/blocklist/history writes.
    5.F1–F2  System writes (shutdown/restart/backup).

  Tier 2 — Missing provider impls (~2-3 days each)
    5.G1–G10 Download clients by traffic (qBittorrent first; long tail last).
    5.H1–H7  Indexers by traffic (FileList first; Fanzub last).

  Tier 3 — Missing read endpoints
    5.I1–I21 Mostly mechanical; group with related Tier-1 slices where natural.

Each slice MUST satisfy the acceptance criteria at PHASE_5_AUDIT.md "Acceptance
criteria for done":
  1. New handler emits correct wire shape (hostbootstrap contract test if a
     golden exists; otherwise unit test pinning the JSON shape).
  2. Writes fire the correct SignalR broadcast.
  3. Writes validate via internal/validation package, emit C# 400 shape on
     failure.
  4. Reads with stable wire shape get a byte-equal contract test against the
     C# golden where one exists in test/contract/golden/v3-empty-state/.
  5. `go test ./...` green.
  6. `golangci-lint run` 0 issues in touched packages.
  7. Per-slice note appended to go/STATUS.md line 9 (use the python lines-array
     pattern other slices use — see slice 4.FW commit for the canonical form).
  8. Commit message follows slice convention, e.g.:
       "Phase 5.B1 (write path): POST/PUT/DELETE /api/v3/tag"
       "Phase 5.G1 (provider impl): qBittorrent download client"
       "Phase 5.A1 (write-path infra): internal/httperr JSON envelope"

Loop instructions:

  Pick ONE slice per iteration. The picker should:

    (a) Read the last 2500 bytes of go/STATUS.md line 9 to see what just landed.
    (b) Read go/PHASE_5_AUDIT.md to find the next un-landed slice ID, top-down
        in the order above.
    (c) Verify dependencies: a Tier-1 slice can NOT land before its foundational
        prereq (5.A1–A5). For example, 5.B1 (tag CRUD) depends on 5.A1
        (httperr) AND 5.A2 (validation) AND 5.A3 (signalr-emitter). If a slice
        is blocked, fall back to its prereq.
    (d) If a Tier-2 (provider impl) slice IS the chosen next one, work via the
        existing sabnzbd/transmission/discord package shapes as templates.
    (e) For ambiguity on C# behaviour, READ THE C# SOURCE under src/ — that is
        the only authoritative reference. Do not invent behaviour.

  Use the strict-diff probe pattern (write internal/api/v3/strict_diff_oneoff_test.go,
  run, surface drift, fix, remove probe) for any write path where a C# golden
  exists. The pattern caught 4 configContract typos in 4.FV — it's a force
  multiplier on accuracy.

  PAUSE conditions (no wakeup, just report):
    - All Tier 1 + Tier 2 + Tier 3 backlog exhausted.
    - A slice surfaces an architectural decision the user must make (e.g.,
      "should validation be per-handler or per-resource?") — schedule no wakeup
      and report the question.
    - A slice requires staging/production access (Phase 6 operational).

Standing rules (carry forward from prior loop prompts):

  - One tight slice per iteration; lint-clean touched packages.
  - No `git push`.
  - Never modify src/ (C# tree is read-only for cite/reference only).
  - No AI attribution on commits (no Co-Authored-By trailers; no Claude credit
    in messages).
  - Append per-slice note to go/STATUS.md line 9.
  - Trust but verify: ALWAYS read the C# source before claiming behaviour.

<<autonomous-loop-dynamic>>
```

---

## How to operate this loop

**To start the next iteration**: copy the block above into a /loop prompt OR pass it as the wakeup `prompt` to `ScheduleWakeup`. The loop will pick the next slice top-down from PHASE_5_AUDIT.md, ship it, append a STATUS note, commit, and schedule the next wakeup.

**To pause for review**: cancel the wakeup. Resume by re-invoking the seed.

**To re-order priorities**: edit PHASE_5_AUDIT.md's "Prioritized fix-mission backlog" section — the loop is keyed off the order in that doc. A topological re-ordering propagates to the next iteration automatically.

**To checkpoint**: after every N slices, consider doing a strict-diff sweep across all known goldens to catch regression drift (see slice 4.FV / 4.FW notes — the strict-diff probe pattern is reproducible).

## Risk notes

- The largest single class of slice (write paths) requires foundational infra (5.A1–A5) to land first. Do not let the loop pick a write slice before the foundation; it'll fail spec-compliance check 3 (validation envelope).
- Tier 2 provider implementations may surface incompatible RPC libraries (e.g., Aria2 uses XML-RPC 1.1; rTorrent uses XML-RPC with specific scgi quirks). Each provider impl IS a real port; budget accordingly.
- Some Tier 3 read endpoints (`log`, `system/routes`, `manualimport`) may not be straightforward — `system/routes` is C# ASP.NET infrastructure with no clean Go analog; `log` reads from C# `NLog` output. PAUSE on these and report the architectural ambiguity rather than guess.
