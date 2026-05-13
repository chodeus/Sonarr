package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// renewURL is the auth.servarr.com token-refresh endpoint. Mirrors C#
// `AniListImportBase.RenewUri` at `AniListImportBase.cs:20`.
//
// Mutable via [SetRenewURLForTest] for httptest seams.
var renewURL = "https://auth.servarr.com/v1/anilist_sonarr/renew"

// SetRenewURLForTest swaps the renew endpoint. Tests redirect outbound
// calls to an httptest.Server.
func SetRenewURLForTest(u string) { renewURL = u }

// httpDoer is the minimal HTTP-client surface the proxy needs.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrRequestFailed wraps any transport-level failure. Mirrors the C#
// HttpException raised by IHttpClient.
var ErrRequestFailed = errors.New("anilist: request failed")

// ErrUnauthorized is the distinguished sentinel for 401.
var ErrUnauthorized = errors.New("anilist: unauthorized")

// isValidRootURL is the Go port of `IsValidUrl` for the ValidRootUrl
// rule.
func isValidRootURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// fetchPage POSTs the QueryList GraphQL document for a single page +
// returns the decoded response. Mirrors C# `AniListRequestGenerator.GetRequest`
// at `:27-45` + the response-deserialise path at
// `AniListParser.ParseResponse:24-60`.
func fetchPage(ctx context.Context, doer httpDoer, settings *Settings, page int) (*pageResponse, error) {
	base := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: empty BaseURL", ErrRequestFailed)
	}
	body := queryBody{
		Query: queryList,
		Variables: queryVariables{
			ID:         settings.Username,
			StatusType: settings.buildStatusFilter(),
			Page:       page,
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %w", ErrRequestFailed, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %w", ErrRequestFailed, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if t := strings.TrimSpace(settings.AccessToken); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %w", ErrRequestFailed, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrRequestFailed, resp.StatusCode, string(out))
	}
	var pr pageResponse
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", ErrRequestFailed, err)
	}
	return &pr, nil
}

// refreshAuthToken GETs the renew endpoint + returns the new token
// bundle. Mirrors C# `AniListImportBase.CheckToken` refresh branch at
// `:78-109`.
func refreshAuthToken(ctx context.Context, doer httpDoer, refreshToken string) (*authToken, error) {
	endpoint := renewURL + "?refresh_token=" + url.QueryEscape(refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build renew: %w", ErrRequestFailed, err)
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: renew do: %w", ErrRequestFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("%w: renew status %d: %s", ErrRequestFailed, resp.StatusCode, string(excerpt))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("%w: read renew: %w", ErrRequestFailed, err)
	}
	var tok authToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("%w: unmarshal renew: %w", ErrRequestFailed, err)
	}
	if tok.AccessToken == "" {
		return nil, nil
	}
	return &tok, nil
}
