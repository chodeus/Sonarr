package downloadclients

import "strings"

// IsLocalhostHost reports whether a download-client `Settings.Host`
// value points at the same machine that's running Sonarr. Mirrors the
// C# pattern at `*DownloadClient*.GetStatus()` (`:253` Deluge, `:255`
// Flood, `:132` DownloadStation, `:253` Transmission, etc.) — the
// canonical check across the tree is `Host == "127.0.0.1" || Host ==
// "localhost"`. The Go-port helper additionally accepts the IPv6
// loopback (`::1`) and trims + lowercases the input so trailing
// whitespace or `LOCALHOST` typed in the UI don't slip past.
//
// Returns false for any host with a non-loopback shape (DNS name,
// public IP, RFC1918 internal IP). Callers that need different
// semantics — e.g. aria2 uses `Host.Contains("127.0.0.1") ||
// Host.Contains("localhost")` per C# `Aria2.cs:197` to allow `host:port`
// forms — must NOT migrate to this helper; their per-DC logic is
// intentional and preserved.
//
// Why this helper exists (audit follow-up J162): every concrete
// Provider's GetStatus is expected to populate
// [ClientInfo.IsLocalhost], and if a new DC author forgets the field
// defaults to false, which causes [healthcheck.ImportMechanismCheck]
// to misclassify a same-host client as remote and surface bogus
// remote-path-mapping warnings. Consolidating the canonical detector
// into one exported helper:
//
//  1. Removes 7 duplicated `isLocalHost` package-private helpers
//     across rqbit / flood / downloadstation / tribler / nzbvortex
//     / freebox / hadouken.
//  2. Gives new DC authors one discoverable place to call instead of
//     reinventing the check (or, worse, forgetting it).
//  3. Centralises the IPv6 + trim + lowercase semantics so future
//     widenings (e.g. accepting `[::1]:6800`) happen in one spot.
//
// The 5 DCs that inline `Host == "127.0.0.1" || EqualFold(Host,
// "localhost")` without the IPv6 loopback (qbittorrent, deluge,
// sabnzbd, transmission, nzbget) preserve the verbatim C# `==`
// posture — migrating them would widen the check and falls under a
// separate "unify localhost detection" sweep tracked in
// [AUDIT_FIX_BACKLOG.md] under "Notable follow-ups".
func IsLocalhostHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
