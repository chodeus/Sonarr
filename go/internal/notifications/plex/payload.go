package plex

import "encoding/json"

// Section ports `PlexSection` at
// `Notifications/Plex/Server/PlexSection.cs:12-27`. Plex returns the
// section ID under the JSON key "key" — wired via tag.
type Section struct {
	ID        int        `json:"key"`
	Type      string     `json:"type"`
	Language  string     `json:"language"`
	Locations []Location `json:"Location"`
}

// Location ports `PlexSectionLocation` at
// `Notifications/Plex/Server/PlexSection.cs:6-10`.
type Location struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

// container is the modern Plex MediaContainer envelope shape.
// Mirrors C# `PlexResponse<PlexSectionsContainer>`.
type container struct {
	MediaContainer struct {
		Sections []Section `json:"Directory"`
	} `json:"MediaContainer"`
}

// legacyContainer is the pre-1.3.1 "_children" envelope shape.
// Mirrors C# `PlexMediaContainerLegacy`.
type legacyContainer struct {
	Children []legacySection `json:"_children"`
}

// legacySection is the pre-1.3.1 section shape — locations live in
// `_children` rather than `Location`.
type legacySection struct {
	ID       int        `json:"key"`
	Type     string     `json:"type"`
	Language string     `json:"language"`
	Children []Location `json:"_children"`
}

// errorContainer is the modern error envelope.
type errorContainer struct {
	MediaContainer struct {
		Error string `json:"error"`
	} `json:"MediaContainer"`
}

// legacyError is the pre-1.3.1 error envelope.
type legacyError struct {
	Error string `json:"error"`
}

// parseSections handles both modern + legacy envelopes — picked on
// the presence of the `_children` key in the response. Filters to
// type=="show" per C# `PlexServerProxy.GetTvSections` at `:45+:59`.
func parseSections(body []byte) ([]Section, error) {
	if hasChildrenKey(body) {
		var lc legacyContainer
		if err := json.Unmarshal(body, &lc); err != nil {
			return nil, err
		}
		out := make([]Section, 0, len(lc.Children))
		for _, ls := range lc.Children {
			if ls.Type != "show" {
				continue
			}
			out = append(out, Section{
				ID: ls.ID, Type: ls.Type, Language: ls.Language, Locations: ls.Children,
			})
		}
		return out, nil
	}
	var c container
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, err
	}
	out := make([]Section, 0, len(c.MediaContainer.Sections))
	for _, s := range c.MediaContainer.Sections {
		if s.Type != "show" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// parseError handles both envelopes; returns the error message or
// empty string when no error is set. Mirrors C# `:151-171`.
func parseError(body []byte) string {
	if hasChildrenKey(body) {
		var le legacyError
		if err := json.Unmarshal(body, &le); err == nil {
			return le.Error
		}
		return ""
	}
	var ec errorContainer
	if err := json.Unmarshal(body, &ec); err == nil {
		return ec.MediaContainer.Error
	}
	return ""
}

// hasChildrenKey reports whether the JSON body uses the legacy
// `_children` shape. Mirrors the C# substring sentinel at
// `PlexServerProxy.cs:41/:82/:161`. Cheap substring scan over the
// raw bytes — no need for a full JSON parse to decide envelope shape.
func hasChildrenKey(body []byte) bool {
	const key = `"_children"`
	if len(body) < len(key) {
		return false
	}
	for i := 0; i+len(key) <= len(body); i++ {
		if string(body[i:i+len(key)]) == key {
			return true
		}
	}
	return false
}
