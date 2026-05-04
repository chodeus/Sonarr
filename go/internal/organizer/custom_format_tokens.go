package organizer

import (
	"strings"

	"sonarr.tv/sonarr/internal/customformats"
)

// RegisterCustomFormatTokens ports `AddCustomFormats` at `:696-714`.
// Registers `{Custom Formats}` and `{Custom Format}`.
//
// `formats` is the list of CustomFormats matching the file. The C#
// version computes this via `_formatCalculator.ParseCustomFormat(file, series)`
// when the caller doesn't supply it; the Go port requires the caller
// to supply it (the calculator is wired at the host boundary). The
// caller passes nil when the file has no formats.
//
// `IncludeCustomFormatWhenRenaming` filters `{Custom Formats}` to
// only those formats marked for inclusion in renames; `{Custom Format}`
// looks up by name regardless and emits empty when no matching format
// has IncludeCustomFormatWhenRenaming=true.
func RegisterCustomFormatTokens(handlers map[string]TokenHandler, formats []*customformats.Format) {
	handlers[NormalizeTokenKey("Custom Formats")] = func(tm TokenMatch) string {
		return getCustomFormatsToken(formats, tm.CustomFormat)
	}
	handlers[NormalizeTokenKey("Custom Format")] = func(tm TokenMatch) string {
		if strings.TrimSpace(tm.CustomFormat) == "" {
			return ""
		}
		for _, f := range formats {
			if f != nil && f.IncludeCustomFormatWhenRenaming && f.Name == tm.CustomFormat {
				return f.Name
			}
		}
		return ""
	}
}

// getCustomFormatsToken ports `GetCustomFormatsToken` at `:724-745`.
// Filters `formats` by `IncludeCustomFormatWhenRenaming`, then by the
// optional `filter` string:
//
//   - filter empty → emit all included formats.
//   - filter starts with `-` → exclude any format whose Name is in
//     the comma-separated list.
//   - else → include only formats whose Name is in the comma-list.
//
// Output is space-joined format names.
func getCustomFormatsToken(formats []*customformats.Format, filter string) string {
	included := make([]*customformats.Format, 0, len(formats))
	for _, f := range formats {
		if f != nil && f.IncludeCustomFormatWhenRenaming {
			included = append(included, f)
		}
	}
	filtered := included
	if strings.TrimSpace(filter) != "" {
		if strings.HasPrefix(filter, "-") {
			excludeSet := splitFilterSet(filter[1:])
			filtered = filtered[:0]
			for _, f := range included {
				if !excludeSet[f.Name] {
					filtered = append(filtered, f)
				}
			}
		} else {
			includeSet := splitFilterSet(filter)
			filtered = filtered[:0]
			for _, f := range included {
				if includeSet[f.Name] {
					filtered = append(filtered, f)
				}
			}
		}
	}
	names := make([]string, 0, len(filtered))
	for _, f := range filtered {
		names = append(names, f.Name)
	}
	return strings.Join(names, " ")
}

func splitFilterSet(filter string) map[string]bool {
	out := make(map[string]bool)
	for _, p := range strings.Split(filter, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out[p] = true
		}
	}
	return out
}
