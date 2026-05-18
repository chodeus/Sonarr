package v3

import (
	"encoding/json"
	"net/http"
	"strings"

	"sonarr.tv/sonarr/internal/parser"
)

// IndexerFlagResource mirrors
// `src/Sonarr.Api.V3/Indexers/IndexerFlagResource.cs`. Three fields:
//
//   - id: the persisted bitmask value (1, 2, 4, 8, ...).
//   - name: the C# enum identifier ("Freeleech", "Halfleech", etc.).
//   - nameLower: the same string lowercased — preserved on the wire
//     because the React UI string-matches against it case-insensitively
//     in places.
type IndexerFlagResource struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	NameLower string `json:"nameLower"`
}

// allIndexerFlags is the static enumeration the GET handler emits.
// Order matches C# `Enum.GetValues(typeof(IndexerFlags))` which is
// declaration order — the bitmask values run 1, 2, 4, 8, 16, 32, 64,
// 128, 256.
var allIndexerFlags = []IndexerFlagResource{
	{ID: int(parser.IndexerFlagFreeleech), Name: "Freeleech"},
	{ID: int(parser.IndexerFlagHalfleech), Name: "Halfleech"},
	{ID: int(parser.IndexerFlagDoubleUpload), Name: "DoubleUpload"},
	{ID: int(parser.IndexerFlagInternal), Name: "Internal"},
	{ID: int(parser.IndexerFlagScene), Name: "Scene"},
	{ID: int(parser.IndexerFlagFreeleech75), Name: "Freeleech75"},
	{ID: int(parser.IndexerFlagFreeleech25), Name: "Freeleech25"},
	{ID: int(parser.IndexerFlagNuked), Name: "Nuked"},
	{ID: int(parser.IndexerFlagSubtitles), Name: "Subtitles"},
}

func init() {
	for i := range allIndexerFlags {
		allIndexerFlags[i].NameLower = strings.ToLower(allIndexerFlags[i].Name)
	}
}

// IndexerFlagHandler returns GET /api/v3/indexerflag. Mirrors
// `IndexerFlagController.GetAll` at
// `src/Sonarr.Api.V3/Indexers/IndexerFlagController.cs:12-18` — emits
// the static `IndexerFlags` enum projection. The list is constant; no
// service dependency.
func IndexerFlagHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		buf, _ := json.Marshal(allIndexerFlags)
		_, _ = w.Write(buf)
	})
}
