// Package runner issues a single corpus Entry against a target HTTP backend
// and produces a normalized Golden ready to be compared or persisted.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"sonarr.tv/sonarr/test/contract/internal/normalize"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// Client issues corpus entries against a target backend.
type Client struct {
	Base   *url.URL
	APIKey string
	HTTP   *http.Client
}

// Run executes a single Entry and returns the normalized Golden.
func (c *Client) Run(ctx context.Context, e spec.Entry) (*spec.Golden, error) {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}

	target := *c.Base
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(e.Request.Path, "/")

	q := target.Query()
	for k, vs := range e.Request.Query {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	target.RawQuery = q.Encode()

	var body io.Reader
	if e.Request.Body != nil {
		b, err := json.Marshal(e.Request.Body)
		if err != nil {
			return nil, fmt.Errorf("entry %s: marshal body: %w", e.ID, err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, e.Request.Method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("entry %s: build request: %w", e.ID, err)
	}
	for k, v := range e.Request.Headers {
		req.Header.Set(k, v)
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("entry %s: do request: %w", e.ID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("entry %s: read response: %w", e.ID, err)
	}

	g := &spec.Golden{
		EntryID: e.ID,
		Status:  resp.StatusCode,
		Headers: normalize.Headers(resp.Header, e.Normalize.RedactHeaders),
	}
	if isJSON(resp.Header.Get("Content-Type")) {
		body, err := normalize.JSON(raw, e.Normalize.RedactJSONPaths)
		if err != nil {
			return nil, fmt.Errorf("entry %s: normalize body: %w", e.ID, err)
		}
		g.BodyJSON = body
		g.BodyIsJSON = true
	} else {
		g.BodyRaw = raw
	}
	return g, nil
}

func isJSON(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "+json")
}
