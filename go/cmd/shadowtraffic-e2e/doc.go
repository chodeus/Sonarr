// Package shadowtraffic_e2e holds the cross-tool integration test for
// the Phase 6.1 shadow-traffic toolkit triad — proves that
// cmd/shadow-traffic-record + cmd/shadow-traffic-replay +
// cmd/shadow-traffic-diff compose end-to-end against real binaries
// (built via `go build` in TestMain, spawned via os/exec).
//
// The test is the integration check that the JSONL log format
// documented in cmd/shadow-traffic-record/FORMAT.md (J245) is what
// the recorder actually writes AND what the replayer actually reads
// AND what the diff binary's downstream caller actually exercises —
// i.e. the format contract is verified end-to-end rather than in
// each binary's unit tests in isolation.
//
// Run via: go test ./cmd/shadowtraffic-e2e/...
package shadowtraffic_e2e
