package quality

import "testing"

// TestFindBySourceAndResolution exercises the three decision branches
// of QualityFinder.FindBySourceAndResolution.
func TestFindBySourceAndResolution(t *testing.T) {
	tests := []struct {
		name string
		src  QualitySource
		res  int
		want Quality
	}{
		// Step 1: exact match.
		{"Television+720 -> HDTV720p", SourceTelevision, 720, HDTV720p},
		{"Television+1080 -> HDTV1080p", SourceTelevision, 1080, HDTV1080p},
		{"Web+720 -> WEBDL720p", SourceWeb, 720, WEBDL720p},
		{"Web+1080 -> WEBDL1080p", SourceWeb, 1080, WEBDL1080p},
		{"Bluray+720 -> Bluray720p", SourceBluray, 720, Bluray720p},
		{"Bluray+1080 -> Bluray1080p", SourceBluray, 1080, Bluray1080p},
		{"Bluray+2160 -> Bluray2160p", SourceBluray, 2160, Bluray2160p},
		{"BlurayRaw+1080 -> Bluray1080pRemux", SourceBlurayRaw, 1080, Bluray1080pRemux},
		{"BlurayRaw+2160 -> Bluray2160pRemux", SourceBlurayRaw, 2160, Bluray2160pRemux},
		{"DVD+480 -> DVD", SourceDVD, 480, DVD},

		// Step 2: sub-720 source-specific fallbacks.
		// 576 has no exact match for Television/Web/WebRip — falls
		// back to the SD equivalent rather than rolling up to Bluray576p.
		{"Television+576 -> SDTV (sub-720 fallback)", SourceTelevision, 576, SDTV},
		{"Web+576 -> WEBDL480p (sub-720 fallback)", SourceWeb, 576, WEBDL480p},
		{"WebRip+576 -> WEBRip480p (sub-720 fallback)", SourceWebRip, 576, WEBRip480p},
		// Bluray+576 has an exact match (Bluray576p) so doesn't go
		// through the fallback.
		{"Bluray+576 -> Bluray576p (exact)", SourceBluray, 576, Bluray576p},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FindBySourceAndResolution(tc.src, tc.res)
			if got.ID != tc.want.ID {
				t.Errorf("FindBySourceAndResolution(%v, %d) = %v, want %v", tc.src, tc.res, got, tc.want)
			}
		})
	}
}
