package videofileinforeader

import (
	"encoding/json"
	"strings"

	"sonarr.tv/sonarr/internal/mediainfo"
)

// Side-data type strings — mirror C#
// `FFMpegCoreSideDataTypes.cs` (constants kept verbatim, including
// the parenthetical variant for HDR10+ that ffprobe emits).
const (
	sideDataDoviConfig         = "DOVI configuration record"
	sideDataHdrDynamicMetadata = "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"
	sideDataMasteringDisplay   = "Mastering display metadata"
	sideDataContentLightLevel  = "Content light level metadata"
)

var (
	validHdrColourPrimaries   = map[string]struct{}{"bt2020": {}}
	hlgTransferFunctions      = map[string]struct{}{"arib-std-b67": {}}
	pqTransferFunctions       = map[string]struct{}{"smpte2084": {}}
	validHdrTransferFunctions = func() map[string]struct{} {
		out := map[string]struct{}{}
		for k := range hlgTransferFunctions {
			out[k] = struct{}{}
		}
		for k := range pqTransferFunctions {
			out[k] = struct{}{}
		}
		return out
	}()
)

// detectHdrFormat ports `VideoFileInfoReader.GetHdrFormat` at
// `:233-283`. The C# version takes a `List<Dictionary<string,
// JsonNode>>` for sideData; the Go-port flattens stream + frame
// side_data_list slices into the `[]map[string]json.RawMessage` shape
// that direct ffprobe JSON unmarshal yields.
//
// Bit-depth gate: <10 → None. Dolby Vision config record always
// trumps; absence falls through to BT.2020 + transfer-function gate
// for HLG / PQ variants.
func detectHdrFormat(bitDepth int, colorPrimaries, transferFunction string, sideData []map[string]json.RawMessage) mediainfo.HdrFormat {
	if bitDepth < 10 {
		return mediainfo.HdrNone
	}

	if dovi, ok := tryGetSideData(sideData, sideDataDoviConfig); ok {
		hasHdr10Plus := false
		if _, found := tryGetSideData(sideData, sideDataHdrDynamicMetadata); found {
			hasHdr10Plus = true
		}
		// dv_bl_signal_compatibility_id is an int in the side-data
		// JSON. Missing / non-int → fallthrough to plain DolbyVision.
		var compat int
		if raw, present := dovi["dv_bl_signal_compatibility_id"]; present {
			_ = json.Unmarshal(raw, &compat)
		}
		switch compat {
		case 1:
			if hasHdr10Plus {
				return mediainfo.HdrDolbyVisionHdr10Plus
			}
			return mediainfo.HdrDolbyVisionHdr10
		case 2:
			return mediainfo.HdrDolbyVisionSdr
		case 4:
			return mediainfo.HdrDolbyVisionHlg
		case 6:
			if hasHdr10Plus {
				return mediainfo.HdrDolbyVisionHdr10Plus
			}
			return mediainfo.HdrDolbyVisionHdr10
		default:
			return mediainfo.HdrDolbyVision
		}
	}

	if _, ok := validHdrColourPrimaries[colorPrimaries]; !ok {
		return mediainfo.HdrNone
	}
	if _, ok := validHdrTransferFunctions[transferFunction]; !ok {
		return mediainfo.HdrNone
	}

	if _, ok := hlgTransferFunctions[transferFunction]; ok {
		return mediainfo.HdrHlg10
	}

	if _, ok := pqTransferFunctions[transferFunction]; ok {
		if _, found := tryGetSideData(sideData, sideDataHdrDynamicMetadata); found {
			return mediainfo.Hdr10Plus
		}
		if _, found := tryGetSideData(sideData, sideDataMasteringDisplay); found {
			return mediainfo.Hdr10
		}
		if _, found := tryGetSideData(sideData, sideDataContentLightLevel); found {
			return mediainfo.Hdr10
		}
		return mediainfo.HdrPq10
	}

	return mediainfo.HdrNone
}

// tryGetSideData returns the first map whose "side_data_type" string
// matches name (case-insensitive — mirrors C# StringComparison.OrdinalIgnoreCase
// at `:289`). The match map is returned alongside an ok flag so
// callers can reach into it for nested fields (DOVI compat ID).
func tryGetSideData(list []map[string]json.RawMessage, name string) (map[string]json.RawMessage, bool) {
	for _, item := range list {
		raw, ok := item["side_data_type"]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if strings.EqualFold(s, name) {
			return item, true
		}
	}
	return nil, false
}
