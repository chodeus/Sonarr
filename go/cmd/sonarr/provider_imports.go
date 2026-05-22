// provider_imports.go blank-imports every concrete import-list and
// notification provider package so their package-level init() blocks
// fire — registering their Constructors with
// `importlists.Default()` and `notifications.Default()` respectively
// during process startup.
//
// Why a dedicated file: every provider follows the same
// self-registration pattern as the indexer + download-client
// families — an `init()` block in each concrete package calls
// `MustRegister` against the per-family global registry. Go only
// runs init() blocks for packages whose symbols are actually
// referenced in the link graph. The cmd/sonarr binary doesn't reach
// for any per-provider symbols directly (everything flows through
// the Registry + Factory abstractions in
// `internal/importlists.Factory` / `internal/notifications.Factory`)
// — so without these blank imports, the registries are empty at
// runtime even though every Constructor is implemented and tested.
//
// Slice 5.J184 (closes the registry-empty-at-runtime caveat from
// slice 5.J183's ImportListSync wiring). The same caveat existed
// for the notifications family — every concrete notifier was
// implemented + tested across earlier Phase 5 slices but the
// notifications Registry stayed empty in production. J184 closes
// both halves together since the import-form is identical and the
// runtime cost is a one-time init() per package at boot (under
// 1 ms total).
//
// Indexers and download-clients: those families register through
// `provider.Default().Replace(provider.FamilyIndexer, ...)` /
// `provider.FamilyDownloadClient` (see e.g.
// `internal/indexers/broadcasthenet/btn.go:516-521` `init()`). The
// indexer registrations already fire from `indexersearch_wiring.go`
// which type-references each concrete indexer package directly —
// no blank import needed there. Same for download clients via
// `downloadclients_lister.go`. Notifications + import-lists are the
// two families that exclusively go through their Registry +
// Factory facade and need explicit blank-import activation.

package main

import (
	// importlists providers — 8 concrete packages, each init() calls
	// `importlists.Default().MustRegister(impl, contract, ctor)`.
	// Order alphabetical to ease audit + future additions.
	_ "sonarr.tv/sonarr/internal/importlists/anilist"
	_ "sonarr.tv/sonarr/internal/importlists/custom"
	_ "sonarr.tv/sonarr/internal/importlists/myanimelist"
	_ "sonarr.tv/sonarr/internal/importlists/plex"
	_ "sonarr.tv/sonarr/internal/importlists/plexrss"
	_ "sonarr.tv/sonarr/internal/importlists/simkl"
	_ "sonarr.tv/sonarr/internal/importlists/sonarr"
	_ "sonarr.tv/sonarr/internal/importlists/trakt"

	// notifications providers — 25 concrete packages, each init()
	// calls `notifications.Default().MustRegister(impl, contract,
	// ctor)`. Order alphabetical.
	_ "sonarr.tv/sonarr/internal/notifications/apprise"
	_ "sonarr.tv/sonarr/internal/notifications/customscript"
	_ "sonarr.tv/sonarr/internal/notifications/discord"
	_ "sonarr.tv/sonarr/internal/notifications/email"
	_ "sonarr.tv/sonarr/internal/notifications/gotify"
	_ "sonarr.tv/sonarr/internal/notifications/join"
	_ "sonarr.tv/sonarr/internal/notifications/mailgun"
	_ "sonarr.tv/sonarr/internal/notifications/mediabrowser"
	_ "sonarr.tv/sonarr/internal/notifications/notifiarr"
	_ "sonarr.tv/sonarr/internal/notifications/ntfy"
	_ "sonarr.tv/sonarr/internal/notifications/plex"
	_ "sonarr.tv/sonarr/internal/notifications/prowl"
	_ "sonarr.tv/sonarr/internal/notifications/pushbullet"
	_ "sonarr.tv/sonarr/internal/notifications/pushcut"
	_ "sonarr.tv/sonarr/internal/notifications/pushover"
	_ "sonarr.tv/sonarr/internal/notifications/sendgrid"
	_ "sonarr.tv/sonarr/internal/notifications/signal"
	_ "sonarr.tv/sonarr/internal/notifications/simplepush"
	_ "sonarr.tv/sonarr/internal/notifications/slack"
	_ "sonarr.tv/sonarr/internal/notifications/synology"
	_ "sonarr.tv/sonarr/internal/notifications/telegram"
	_ "sonarr.tv/sonarr/internal/notifications/trakt"
	_ "sonarr.tv/sonarr/internal/notifications/twitter"
	_ "sonarr.tv/sonarr/internal/notifications/webhook"
	_ "sonarr.tv/sonarr/internal/notifications/xbmc"
)
