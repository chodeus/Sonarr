package discord

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmbedJSON_NilPointersOmitKeys is the byte-level guard for the
// J170 wire-shape parity: a default Embed with nil Author /
// Thumbnail / Image must NOT emit those keys. C# Sonarr serializes
// `DiscordPayload` via Newtonsoft's `ToJson` extension at
// `src/NzbDrone.Common/Serializer/Newtonsoft.Json/Json.cs:119`,
// which uses the shared `SerializerSettings` configured with
// `NullValueHandling.Ignore` at `Json.cs:26`. Null reference
// properties (DiscordAuthor / DiscordImage in `Embed.cs:12-14`)
// are dropped from the wire body. The Go port matches via
// `omitempty` on the three pointer fields.
func TestEmbedJSON_NilPointersOmitKeys(t *testing.T) {
	b, err := json.Marshal(Embed{Title: "hello"})
	if err != nil {
		t.Fatalf("marshal embed: %v", err)
	}
	got := string(b)
	for _, key := range []string{`"author"`, `"thumbnail"`, `"image"`} {
		if strings.Contains(got, key) {
			t.Errorf("default Embed must omit %s key; got %s", key, got)
		}
	}
}

// TestEmbedJSON_NonNilPointersEmitKeys confirms the opposite branch:
// when the three pointer fields are populated they DO appear on the
// wire (omitempty doesn't suppress non-nil pointers).
func TestEmbedJSON_NonNilPointersEmitKeys(t *testing.T) {
	embed := Embed{
		Author:    &Author{Name: "sonarr", IconURL: "https://example/icon.png"},
		Thumbnail: &Image{URL: "https://example/poster.jpg"},
		Image:     &Image{URL: "https://example/fanart.jpg"},
	}
	b, err := json.Marshal(embed)
	if err != nil {
		t.Fatalf("marshal embed: %v", err)
	}
	got := string(b)
	for _, want := range []string{
		`"author":{`,
		`"thumbnail":{"url":"https://example/poster.jpg"}`,
		`"image":{"url":"https://example/fanart.jpg"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("populated Embed must contain %s; got %s", want, got)
		}
	}
}
