package indexers

// DownloadProtocol mirrors the C# enum at
// `src/NzbDrone.Core/Indexers/DownloadProtocol.cs:1-9`. Tags an indexer
// (and any release returned by an indexer) as Usenet vs Torrent.
//
// Persisted: as the IndexerDefinition.Protocol column? No — that field
// is NOT persisted; it's derived at service-layer wiring time from the
// indexer's Implementation class. This enum DOES flow through the v3
// JSON API on every release row, however, so order is wire-stable
// against the React frontend's per-int lookup. Re-ordering breaks the
// frontend's "Usenet" vs "Torrent" badge rendering.
type DownloadProtocol int

// DownloadProtocol enumeration. Values pinned against C# `:5-7`.
const (
	// DownloadProtocolUnknown is the zero value — releases pushed
	// before any indexer was wired up land here.
	DownloadProtocolUnknown DownloadProtocol = 0
	// DownloadProtocolUsenet covers Newznab + similar feed types.
	DownloadProtocolUsenet DownloadProtocol = 1
	// DownloadProtocolTorrent covers Torznab + similar feed types.
	DownloadProtocolTorrent DownloadProtocol = 2
)

// String returns the lower-case wire form. The C# JSON converter
// emits PascalCase ("Usenet"/"Torrent"); the Go port lowercases for
// consistency with other domain enums (RejectionKind, Result) — the
// v3 API surface will translate at the boundary if needed.
func (p DownloadProtocol) String() string {
	switch p {
	case DownloadProtocolUsenet:
		return "usenet"
	case DownloadProtocolTorrent:
		return "torrent"
	}
	return "unknown"
}
