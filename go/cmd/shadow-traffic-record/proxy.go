package main

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"sonarr.tv/sonarr/cmd/internal/shadowtraffic/httputil"
)

// RecordingProxy is the http.Handler that fronts a single upstream
// backend. Each inbound request is captured into the LogWriter (best
// effort) AND dispatched to the upstream (return-to-caller). The
// recording happens BEFORE dispatch so the captured timestamp reflects
// the moment the request entered the proxy, not when the upstream
// responded — this aligns with cmd/shadow-traffic-replay's pacing
// model (inter-entry-timestamp delta drives replay cadence).
type RecordingProxy struct {
	Upstream    *url.URL
	Writer      *LogWriter
	DropAPIKeys bool // strip X-Api-Key + Authorization before logging
	Logger      *slog.Logger

	requests atomic.Int64
	failures atomic.Int64

	// httpClient is injectable for tests; defaults set lazily.
	httpClient *http.Client
}

// ProxyStats — atomic counters for the request hot path.
type ProxyStats struct {
	Requests int64
	Failures int64
}

// Stats returns a snapshot.
func (p *RecordingProxy) Stats() ProxyStats {
	return ProxyStats{
		Requests: p.requests.Load(),
		Failures: p.failures.Load(),
	}
}

func (p *RecordingProxy) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func (p *RecordingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.failures.Add(1)
		http.Error(w, "shadow-traffic-record: failed to buffer request body", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// Dispatch to upstream.
	upstreamURL := *p.Upstream
	upstreamURL.Path = httputil.SingleJoin(p.Upstream.Path, r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		p.failures.Add(1)
		http.Error(w, "shadow-traffic-record: build upstream request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	for k, vs := range r.Header {
		if httputil.IsHopHeader(k) {
			continue
		}
		for _, v := range vs {
			outReq.Header.Add(k, v)
		}
	}
	outReq.Host = ""

	resp, err := p.client().Do(outReq)
	if err != nil {
		p.failures.Add(1)
		// Still record the request — operator wants to see what
		// hit the proxy even if upstream fails. Status=0 marks
		// the upstream-failure case.
		p.record(r, body, 0)
		http.Error(w, "shadow-traffic-record: upstream failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy response headers minus hop-by-hop + Content-Length.
	for k, vs := range resp.Header {
		if httputil.IsHopHeader(k) || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the body to the client. Use io.Copy rather than
	// ReadAll+Write — large responses (full library refresh, log
	// download) shouldn't be buffered through this proxy.
	if _, err := io.Copy(w, resp.Body); err != nil {
		p.Logger.Warn("response copy failed", "err", err, "path", r.URL.Path)
	}

	// Record AFTER the client stream completes — the
	// upstream-status is now known. Enqueue is non-blocking.
	p.record(r, body, resp.StatusCode)
}

func (p *RecordingProxy) record(r *http.Request, body []byte, status int) {
	if p.Writer == nil {
		return
	}
	logBody, decompressed := maybeDecompressForLog(body, r.Header.Get("Content-Encoding"), decompressLimitFor(p.Writer))
	if !decompressed && r.Header.Get("Content-Encoding") != "" && strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") && len(body) > 0 && p.Logger != nil {
		p.Logger.Warn("gzip request body decompression skipped; logging verbatim", "path", r.URL.Path, "len", len(body))
	}
	entry := RecordedEntry{
		Timestamp: time.Now().UTC(),
		Method:    r.Method,
		Path:      r.URL.RequestURI(),
		Headers:   stripHopHeaders(r.Header, p.DropAPIKeys, decompressed),
		Body:      string(logBody),
		Status:    status,
	}
	p.Writer.Enqueue(entry)
}

// decompressLimitFor derives the decompression-bomb cap from the
// writer's configured max body length: same as maxBodyLen when set
// (the recorder already truncates above that anyway), otherwise an
// 8 MiB sane default. The cap protects against malicious gzip bombs
// — a few-KB gzip can expand to GiB. When exceeded, the recorder
// falls back to logging the gzipped bytes verbatim.
func decompressLimitFor(w *LogWriter) int64 {
	if w == nil {
		return 8 * 1024 * 1024
	}
	if w.maxBodyLen > 0 {
		return int64(w.maxBodyLen)
	}
	return 8 * 1024 * 1024
}
