package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"sonarr.tv/sonarr/cmd/internal/shadowtraffic/httputil"
)

// DualProxy is the http.Handler that dispatches each incoming request to
// both upstream backends in parallel. The primary backend's response is
// streamed back to the caller; the secondary's body is buffered and fed
// to the Differ, which records any diff against the Report.
//
// Failure-mode handling:
//
//   - Primary unreachable / 5xx → return 502 to caller, record DIVERGE+highSev.
//   - Secondary unreachable / timeout → return primary verbatim, record
//     DIVERGE+highSev with the secondary's failure description.
//   - Both reach 200 with byte-equal body → record OK (no diff).
//   - 200 vs 200 with body diff post-redact → record DIVERGE+lowSev for
//     value-only mismatches, DIVERGE+highSev for kind/status mismatches.
type DualProxy struct {
	Primary         *url.URL
	Secondary       *url.URL
	PrimaryLabel    string
	SecondaryLabel  string
	DispatchTimeout time.Duration
	Differ          *Differ
	Report          *Report
	Logger          *slog.Logger

	// httpClient is injectable for tests; defaults are set lazily.
	httpClient *http.Client
}

func (p *DualProxy) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	// Default client with conservative transport settings. The
	// dispatch timeout is applied at the per-request context level
	// rather than Client.Timeout so that the diff path can still
	// see partial responses.
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// upstreamResp captures the buffered backend response for diffing.
type upstreamResp struct {
	label   string
	status  int
	headers http.Header
	body    []byte
	err     error
	took    time.Duration
}

func (p *DualProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read the inbound body once so it can be replayed to both backends.
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "shadow-traffic-diff: failed to buffer request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	ctx, cancel := context.WithTimeout(r.Context(), p.DispatchTimeout)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	var primaryResp, secondaryResp upstreamResp

	go func() {
		defer wg.Done()
		primaryResp = p.dispatch(ctx, p.PrimaryLabel, p.Primary, r, reqBody)
	}()
	go func() {
		defer wg.Done()
		secondaryResp = p.dispatch(ctx, p.SecondaryLabel, p.Secondary, r, reqBody)
	}()
	wg.Wait()

	// Stream primary's response back to caller (or 502 on primary
	// failure). The diff record happens after the stream completes so
	// that latency added to the client request is minimised.
	if primaryResp.err != nil {
		p.Logger.Error("primary dispatch failed",
			"label", primaryResp.label,
			"path", r.URL.Path,
			"err", primaryResp.err,
		)
		http.Error(w, "shadow-traffic-diff: primary backend failed: "+primaryResp.err.Error(), http.StatusBadGateway)
		// Still record the diff for analysis (highSev — primary down
		// is the most important signal during a shadow period).
		p.Report.Record(buildDiffRow(r, primaryResp, secondaryResp, []string{"primary backend failed: " + primaryResp.err.Error()}, SeverityHigh))
		return
	}

	// Copy headers minus hop-by-hop and Content-Length (which the
	// stdlib's response writer re-derives from the bytes we write).
	for k, vs := range primaryResp.headers {
		if httputil.IsHopHeader(k) || k == "Content-Length" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(primaryResp.status)
	_, _ = w.Write(primaryResp.body)

	// Diff after the client stream completes — never block the caller.
	go p.recordDiff(r, primaryResp, secondaryResp)
}

func (p *DualProxy) dispatch(ctx context.Context, label string, target *url.URL, src *http.Request, reqBody []byte) upstreamResp {
	start := time.Now()
	u := *target
	u.Path = httputil.SingleJoin(target.Path, src.URL.Path)
	u.RawQuery = src.URL.RawQuery

	req, err := http.NewRequestWithContext(ctx, src.Method, u.String(), bytes.NewReader(reqBody))
	if err != nil {
		return upstreamResp{label: label, err: err, took: time.Since(start)}
	}
	for k, vs := range src.Header {
		if httputil.IsHopHeader(k) {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Drop Host so the http.Transport sets it from target.
	req.Host = ""

	resp, err := p.client().Do(req)
	if err != nil {
		return upstreamResp{label: label, err: err, took: time.Since(start)}
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return upstreamResp{label: label, status: resp.StatusCode, headers: resp.Header.Clone(), err: err, took: time.Since(start)}
	}

	return upstreamResp{
		label:   label,
		status:  resp.StatusCode,
		headers: resp.Header.Clone(),
		body:    body,
		took:    time.Since(start),
	}
}

func (p *DualProxy) recordDiff(r *http.Request, primary, secondary upstreamResp) {
	// Secondary-side failure: high severity DIVERGE.
	if secondary.err != nil {
		p.Report.Record(buildDiffRow(r, primary, secondary,
			[]string{"secondary backend failed: " + secondary.err.Error()},
			SeverityHigh,
		))
		return
	}

	// Status-code mismatch is always high.
	if primary.status != secondary.status {
		p.Report.Record(buildDiffRow(r, primary, secondary,
			[]string{statusMismatchSummary(primary, secondary)},
			SeverityHigh,
		))
		return
	}

	// Body diff via the normalize package.
	diffs, severity, err := p.Differ.Diff(r.URL.Path, primary.body, secondary.body)
	if err != nil {
		// Diff itself failed — treat as high so the operator sees it.
		p.Report.Record(buildDiffRow(r, primary, secondary,
			[]string{"diff failed: " + err.Error()},
			SeverityHigh,
		))
		return
	}
	if len(diffs) == 0 {
		// Byte-equal post-redact — record at OK severity for
		// throughput tracking but don't pollute the diff report.
		p.Report.RecordOK(r.Method, r.URL.Path, primary.status)
		return
	}
	p.Report.Record(buildDiffRow(r, primary, secondary, diffs, severity))
}

func buildDiffRow(r *http.Request, primary, secondary upstreamResp, diffs []string, sev Severity) DiffRow {
	return DiffRow{
		Timestamp:        time.Now().UTC(),
		Method:           r.Method,
		Path:             r.URL.Path,
		PrimaryStatus:    primary.status,
		SecondaryStatus:  secondary.status,
		PrimaryLatency:   primary.took,
		SecondaryLatency: secondary.took,
		PrimaryLabel:     primary.label,
		SecondaryLabel:   secondary.label,
		Diffs:            diffs,
		Severity:         sev,
	}
}

func statusMismatchSummary(primary, secondary upstreamResp) string {
	return "status mismatch: " + primary.label + "=" + itoa(primary.status) + " vs " + secondary.label + "=" + itoa(secondary.status)
}

// itoa avoids strconv import for a single-call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ErrSecondaryTimeout signals the secondary backend didn't respond
// within the dispatch deadline. Used by tests to assert timeout
// handling shape.
var ErrSecondaryTimeout = errors.New("secondary backend timed out")
