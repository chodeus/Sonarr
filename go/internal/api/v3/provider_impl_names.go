package v3

// G4 follow-up (J443): the provider-resource mappers (notification /
// indexer / importlist / downloadclient / metadata) previously set
// `ImplementationName: row.Implementation` — a stub copy. C# emits the
// human-readable name, which DIFFERS for several impls (e.g. "Nzbget"→
// "NZBGet", "UsenetDownloadStation"→"Download Station", "QBittorrent"→
// "qBittorrent", "Xbmc"→"Kodi (XBMC) / Emby"). The correct names already
// live in each family's schema table; these maps index them by
// Implementation so the GET mappers can look them up.
//
// Fallback to the raw Implementation when a row's impl isn't in the
// schema map keeps behaviour unchanged for matching names and leaves the
// empty-DB contract goldens byte-equal (no rows → mappers never run).

// implNameMapFrom builds an Implementation→ImplementationName index from
// a family's schema list. Entries with an empty implementation or name
// are skipped.
func implNameMapFrom[T any](schemas []T, impl func(T) string, name func(T) string) map[string]string {
	m := make(map[string]string, len(schemas))
	for _, s := range schemas {
		i, n := impl(s), name(s)
		if i != "" && n != "" {
			m[i] = n
		}
	}
	return m
}

// implementationNameOr returns the schema-sourced ImplementationName for
// impl, or impl itself when there's no mapping.
func implementationNameOr(m map[string]string, impl string) string {
	if n, ok := m[impl]; ok && n != "" {
		return n
	}
	return impl
}

var notificationImplNames = implNameMapFrom(notificationSchemas(),
	func(s NotificationSchemaResource) string { return s.Implementation },
	func(s NotificationSchemaResource) string { return s.ImplementationName })

var indexerImplNames = implNameMapFrom(indexerSchemas(),
	func(s IndexerSchemaResource) string { return s.Implementation },
	func(s IndexerSchemaResource) string { return s.ImplementationName })

var downloadClientImplNames = implNameMapFrom(downloadClientSchemas(),
	func(s DownloadClientSchemaResource) string { return s.Implementation },
	func(s DownloadClientSchemaResource) string { return s.ImplementationName })

var importListImplNames = implNameMapFrom(importListSchemas,
	func(s ImportListSchemaResource) string { return s.Implementation },
	func(s ImportListSchemaResource) string { return s.ImplementationName })

var metadataImplNames = implNameMapFrom(metadataSchemas,
	func(s MetadataSchemaResource) string { return s.Implementation },
	func(s MetadataSchemaResource) string { return s.ImplementationName })
