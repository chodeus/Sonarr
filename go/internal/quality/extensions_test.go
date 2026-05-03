package quality

import "testing"

// TestGetQualityForExtension covers the extension-to-quality lookup.
// Selected entries from each Quality bucket plus negative cases.
func TestGetQualityForExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want Quality
	}{
		// Unknown bucket — webm is explicitly mapped to Unknown.
		{".webm", Unknown},

		// SDTV bucket sample.
		{".avi", SDTV},
		{".m4v", SDTV},
		{".divx", SDTV},
		{".mp4", SDTV},
		{".dvr-ms", SDTV},

		// DVD bucket.
		{".img", DVD},
		{".iso", DVD},
		{".vob", DVD},

		// HD bucket.
		{".mkv", HDTV720p},
		{".ts", HDTV720p},
		{".wtv", HDTV720p},

		// Bluray bucket.
		{".m2ts", Bluray720p},

		// Case-insensitive lookup.
		{".MKV", HDTV720p},
		{".AVI", SDTV},
		{".M2TS", Bluray720p},

		// Negative cases.
		{"", Unknown},
		{".unknown_ext", Unknown},
		{"mkv", Unknown}, // no leading dot
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.ext, func(t *testing.T) {
			t.Parallel()
			got := GetQualityForExtension(tc.ext)
			if got.ID != tc.want.ID {
				t.Errorf("GetQualityForExtension(%q) = %v, want %v", tc.ext, got, tc.want)
			}
		})
	}
}
