package nyaa

import "net/url"

// isValidRootURL ports the same shape as the sibling-indexer
// helpers — non-empty, scheme http(s), host present.
func isValidRootURL(s string) bool {
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}
