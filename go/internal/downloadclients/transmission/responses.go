package transmission

// TransmissionTorrentStatus mirrors C# `TransmissionTorrentStatus`
// at `TransmissionTorrentStatus.cs:1-10`. Wire form is the integer
// ordinal; the C# enum has no [JsonConverter] attribute, so the
// default Newtonsoft behaviour emits/consumes integers.
type TransmissionTorrentStatus int

// TransmissionTorrentStatus constants. Order + values mirror C#.
const (
	TorrentStatusStopped     TransmissionTorrentStatus = 0
	TorrentStatusCheckWait   TransmissionTorrentStatus = 1
	TorrentStatusCheck       TransmissionTorrentStatus = 2
	TorrentStatusQueued      TransmissionTorrentStatus = 3
	TorrentStatusDownloading TransmissionTorrentStatus = 4
	TorrentStatusSeedingWait TransmissionTorrentStatus = 5
	TorrentStatusSeeding     TransmissionTorrentStatus = 6
)

// TransmissionTorrent mirrors C# `TransmissionTorrent` at
// `TransmissionTorrent.cs:1-30`. Field names use camelCase to match
// the Transmission RPC wire shape. The two file-count fields
// (TransmissionFileCount + VuzeFileCount) survive because Vuze
// reuses TransmissionSettings and emits its file count under the
// `fileCount` JSON tag instead of `file-count`.
type TransmissionTorrent struct {
	ID             int                       `json:"id"`
	HashString     string                    `json:"hashString"`
	Name           string                    `json:"name"`
	DownloadDir    string                    `json:"downloadDir"`
	TotalSize      int64                     `json:"totalSize"`
	LeftUntilDone  int64                     `json:"leftUntilDone"`
	IsFinished     bool                      `json:"isFinished"`
	Labels         []string                  `json:"labels"`
	Eta            int64                     `json:"eta"`
	Status         TransmissionTorrentStatus `json:"status"`
	SecondsDLing   int64                     `json:"secondsDownloading"`
	SecondsSeeding int64                     `json:"secondsSeeding"`
	ErrorString    string                    `json:"errorString"`
	DownloadedEver int64                     `json:"downloadedEver"`
	UploadedEver   int64                     `json:"uploadedEver"`
	SeedRatioLimit float64                   `json:"seedRatioLimit"`
	SeedRatioMode  int                       `json:"seedRatioMode"`
	SeedIdleLimit  int64                     `json:"seedIdleLimit"`
	SeedIdleMode   int                       `json:"seedIdleMode"`

	// Transmission emits the file-count under `file-count`; Vuze
	// emits it under `fileCount`. Either may be populated; consumers
	// read [TransmissionTorrent.FileCount] which prefers the
	// Transmission tag.
	TransmissionFileCount *int `json:"file-count"`
	VuzeFileCount         *int `json:"fileCount"`
}

// FileCount returns the file count, preferring the Transmission tag.
// Mirrors the C# computed property at `TransmissionTorrent.cs:27`.
func (t TransmissionTorrent) FileCount() int {
	if t.TransmissionFileCount != nil {
		return *t.TransmissionFileCount
	}
	if t.VuzeFileCount != nil {
		return *t.VuzeFileCount
	}
	return 0
}

// TransmissionConfig mirrors C# `TransmissionConfig` at
// `TransmissionConfig.cs:1-21`. The wire form uses kebab-case for
// the configurable-by-user fields (download-dir, idle-seeding-limit,
// etc.) and camelCase for the rest.
type TransmissionConfig struct {
	RpcVersion              string  `json:"rpc-version"`
	Version                 string  `json:"version"`
	DownloadDir             string  `json:"download-dir"`
	SeedRatioLimit          float64 `json:"seedRatioLimit"`
	SeedRatioLimited        bool    `json:"seedRatioLimited"`
	IdleSeedingLimit        int64   `json:"idle-seeding-limit"`
	IdleSeedingLimitEnabled bool    `json:"idle-seeding-limit-enabled"`
}

// torrentGetResponse is the typed `arguments` payload for the
// torrent-get RPC. The Transmission RPC docs guarantee the top-level
// key is `torrents`.
type torrentGetResponse struct {
	Torrents []TransmissionTorrent `json:"torrents"`
}
