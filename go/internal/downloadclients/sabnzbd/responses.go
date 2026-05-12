package sabnzbd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SabnzbdDownloadStatus mirrors C# `SabnzbdDownloadStatus` at
// `SabnzbdDownloadStatus.cs:1-22`. SAB emits the enum value as the
// string name (e.g. "Downloading", "Completed", "Paused"); the C#
// default Newtonsoft enum converter deserializes name→value.
//
// The Go port keeps the value as a string-typed enum because most
// consumers (GetItems / RemoveItem) only compare against named
// constants — int-bbacking the type would force every consumer to
// import the converter helper. The named constants below cover every
// status the C# tree spells out at `SabnzbdDownloadStatus.cs`.
type SabnzbdDownloadStatus string

// SabnzbdDownloadStatus constants. Names match the C# enum names
// 1-for-1 — SAB's JSON output is case-sensitive on these names.
const (
	SabStatusGrabbing    SabnzbdDownloadStatus = "Grabbing"
	SabStatusQueued      SabnzbdDownloadStatus = "Queued"
	SabStatusPaused      SabnzbdDownloadStatus = "Paused"
	SabStatusChecking    SabnzbdDownloadStatus = "Checking"
	SabStatusDownloading SabnzbdDownloadStatus = "Downloading"
	SabStatusQuickCheck  SabnzbdDownloadStatus = "QuickCheck"
	SabStatusVerifying   SabnzbdDownloadStatus = "Verifying"
	SabStatusRepairing   SabnzbdDownloadStatus = "Repairing"
	SabStatusFetching    SabnzbdDownloadStatus = "Fetching"
	SabStatusExtracting  SabnzbdDownloadStatus = "Extracting"
	SabStatusMoving      SabnzbdDownloadStatus = "Moving"
	SabStatusRunning     SabnzbdDownloadStatus = "Running"
	SabStatusCompleted   SabnzbdDownloadStatus = "Completed"
	SabStatusFailed      SabnzbdDownloadStatus = "Failed"
	SabStatusDeleted     SabnzbdDownloadStatus = "Deleted"
	SabStatusPropagating SabnzbdDownloadStatus = "Propagating"
)

// queueTimeleft mirrors the C# `SabnzbdQueueTimeConverter` at
// `JsonConverters/SabnzbdQueueTimeConverter.cs:1-34`. SAB emits the
// remaining time as "HH:MM:SS" or "D:HH:MM:SS" — we keep the parsed
// value as a [time.Duration].
type queueTimeleft time.Duration

// UnmarshalJSON parses "HH:MM:SS" or "D:HH:MM:SS" into a Duration.
// Empty or null input → zero Duration. Invalid → error.
func (q *queueTimeleft) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*q = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("sabnzbd: queueTimeleft: %w", err)
	}
	if strings.TrimSpace(s) == "" {
		*q = 0
		return nil
	}
	parts := strings.Split(s, ":")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return fmt.Errorf("sabnzbd: queueTimeleft: parse %q: %w", s, err)
		}
		nums[i] = n
	}
	var d time.Duration
	switch len(nums) {
	case 4:
		d = time.Duration(nums[0])*24*time.Hour +
			time.Duration(nums[1])*time.Hour +
			time.Duration(nums[2])*time.Minute +
			time.Duration(nums[3])*time.Second
	case 3:
		d = time.Duration(nums[0])*time.Hour +
			time.Duration(nums[1])*time.Minute +
			time.Duration(nums[2])*time.Second
	default:
		return fmt.Errorf("sabnzbd: queueTimeleft: expected H:M:S or D:H:M:S, got %q", s)
	}
	*q = queueTimeleft(d)
	return nil
}

// Duration returns the time.Duration form for callers.
func (q queueTimeleft) Duration() time.Duration { return time.Duration(q) }

// stringArrayOrString mirrors C# `SabnzbdStringArrayConverter` at
// `JsonConverters/SabnzbdStringArrayConverter.cs:1-50`. SAB sometimes
// emits a single-element array as a bare string; this type accepts
// either form.
type stringArrayOrString []string

// UnmarshalJSON accepts a JSON string or array-of-string and stores
// it as a slice. Empty input → empty slice. null → empty slice.
func (s *stringArrayOrString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*s = nil
		return nil
	}
	if data[0] == '"' {
		var one string
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*s = []string{one}
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	}
	return errors.New("sabnzbd: stringArrayOrString: expected string or array")
}

// SabnzbdQueueItem mirrors C# `SabnzbdQueueItem` at
// `SabnzbdQueueItem.cs:1-34`. The JSON tags use SAB's wire names.
type SabnzbdQueueItem struct {
	Status     SabnzbdDownloadStatus `json:"status"`
	Index      int                   `json:"index"`
	Timeleft   queueTimeleft         `json:"timeleft"`
	SizeMB     float64               `json:"mb"`
	Title      string                `json:"filename"`
	Priority   string                `json:"priority"`
	Category   string                `json:"cat"`
	SizeLeftMB float64               `json:"mbleft"`
	Percentage int                   `json:"percentage"`
	ID         string                `json:"nzo_id"`
}

// IsPriorityPaused reports whether SAB's per-item priority is Paused.
// Mirrors the C# enum-value comparison used at `Sabnzbd.cs:79`. The
// wire value is the enum name ("Paused"), matching the C#
// `SabnzbdPriorityTypeConverter`.
func (q SabnzbdQueueItem) IsPriorityPaused() bool { return q.Priority == "Paused" }

// IsPriorityForce reports whether SAB's per-item priority is Force.
// Used at `Sabnzbd.cs:78`.
func (q SabnzbdQueueItem) IsPriorityForce() bool { return q.Priority == "Force" }

// SabnzbdQueue mirrors C# `SabnzbdQueue` at `SabnzbdQueue.cs:1-17`.
// DefaultRootFolder was removed in SAB 2.0; queue items source it
// from [SabnzbdFullStatus.CompleteDir] instead.
type SabnzbdQueue struct {
	DefaultRootFolder string             `json:"my_home"`
	Paused            bool               `json:"paused"`
	Items             []SabnzbdQueueItem `json:"slots"`
}

// queueResponse is the outer JSON envelope SAB wraps GetQueue in:
// `{"queue": {...}}`. Mirrors the C# `JObject.Parse(response)
// .SelectToken("queue")` extraction at `SabnzbdProxy.cs:129`.
type queueResponse struct {
	Queue SabnzbdQueue `json:"queue"`
}

// SabnzbdHistoryItem mirrors C# `SabnzbdHistoryItem` at
// `SabnzbdHistoryItem.cs:1-29`.
type SabnzbdHistoryItem struct {
	FailMessage  string                `json:"fail_message"`
	Size         int64                 `json:"bytes"`
	Category     string                `json:"category"`
	NzbName      string                `json:"nzb_name"`
	DownloadTime int                   `json:"download_time"`
	Storage      string                `json:"storage"`
	Status       SabnzbdDownloadStatus `json:"status"`
	ID           string                `json:"nzo_id"`
	Title        string                `json:"name"`
}

// SabnzbdHistory mirrors C# `SabnzbdHistory` at `SabnzbdHistory.cs:1-14`.
type SabnzbdHistory struct {
	Paused bool                 `json:"paused"`
	Items  []SabnzbdHistoryItem `json:"slots"`
}

// historyResponse is the outer JSON envelope: `{"history": {...}}`.
// Mirrors C# `:145`.
type historyResponse struct {
	History SabnzbdHistory `json:"history"`
}

// SabnzbdConfigMisc carries the [SabnzbdConfig.Misc] block — the SAB
// global-settings sub-tree that GetCategories + GetStatus read.
type SabnzbdConfigMisc struct {
	CompleteDir          string              `json:"complete_dir"`
	TvCategories         []string            `json:"tv_categories"`
	EnableTvSorting      bool                `json:"enable_tv_sorting"`
	MovieCategories      []string            `json:"movie_categories"`
	EnableMovieSorting   bool                `json:"enable_movie_sorting"`
	DateCategories       stringArrayOrString `json:"date_categories"`
	EnableDateSorting    bool                `json:"enable_date_sorting"`
	PreCheck             bool                `json:"pre_check"`
	HistoryRetention     string              `json:"history_retention"`
	HistoryRetentionOpt  string              `json:"history_retention_option"`
	HistoryRetentionDays int                 `json:"history_retention_number"`
}

// SabnzbdCategory mirrors C# `SabnzbdCategory` at
// `SabnzbdCategory.cs:1-67` — the per-category sub-row exposed in
// the [SabnzbdConfig.Categories] list.
type SabnzbdCategory struct {
	Priority int    `json:"priority"`
	PP       string `json:"pp"`
	Name     string `json:"name"`
	Script   string `json:"script"`
	Dir      string `json:"dir"`
	// FullPath is populated by the Go-side helper that resolves
	// relative Dir values against SAB's complete_dir. Not part of
	// the SAB wire shape.
	FullPath string `json:"-"`
}

// SabnzbdSorter mirrors C# `SabnzbdSorter`.
type SabnzbdSorter struct {
	Name           string   `json:"name"`
	Order          int      `json:"order"`
	MinSize        string   `json:"min_size"`
	MultipartLabel string   `json:"multipart_label"`
	SortString     string   `json:"sort_string"`
	SortCategories []string `json:"sort_cats"`
	SortType       []int    `json:"sort_type"`
	IsActive       bool     `json:"is_active"`
}

// SabnzbdConfig mirrors C# `SabnzbdConfig` at `SabnzbdConfig.cs`.
type SabnzbdConfig struct {
	Misc       SabnzbdConfigMisc `json:"misc"`
	Categories []SabnzbdCategory `json:"categories"`
	Sorters    []SabnzbdSorter   `json:"sorters"`
}

// configResponse is the outer JSON envelope: `{"config": {...}}`.
// Mirrors C# `SabnzbdConfigResponse`.
type configResponse struct {
	Config SabnzbdConfig `json:"config"`
}

// SabnzbdFullStatus mirrors C# `SabnzbdFullStatus` at
// `SabnzbdFullStatus.cs:1-12`.
type SabnzbdFullStatus struct {
	CompleteDir string `json:"completedir"`
}

// fullStatusResponse is the outer JSON envelope: `{"status": {...}}`.
type fullStatusResponse struct {
	Status SabnzbdFullStatus `json:"status"`
}
