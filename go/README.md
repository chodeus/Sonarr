# Sonarr Backend — Go Port

This tree holds the Go reimplementation of the Sonarr v5 backend. While the port is in progress, the existing C# codebase under `../src/` continues to build, ship, and accept feature work; the Go binary will replace it in a single cutover commit once parity is reached and the contract-test suite is fully green.

The conversion plan and rules are authoritative — see [`CONVERSION.md`](CONVERSION.md).

## Layout

```
go/
  CONVERSION.md           # the conversion rules every PR follows
  go.mod                  # module sonarr.tv/sonarr
  cmd/
    sonarr/               # main daemon (replaces NzbDrone + NzbDrone.Console + NzbDrone.Host)
    sonarr-update/        # updater (replaces NzbDrone.Update)
  internal/
    common/               # ← NzbDrone.Common
    config/               # typed config + config.xml backwards-compat reader
    datastore/            # db open, migrations, repo helpers
    tv/  parser/  qualities/  languages/  customformats/
    indexers/  download/  importlists/  notifications/  metadata/
    mediafiles/  organizer/  decisionengine/  indexersearch/
    queue/  history/  blocklist/
    autotagging/  tags/  rootfolders/  remotepathmappings/
    healthcheck/  housekeeping/  jobs/  lifecycle/  backup/
    mediacover/  messaging/  signalr/  auth/  update/
  api/
    v3/  v5/              # mirror src/Sonarr.Api.V3, src/Sonarr.Api.V5 route-for-route
    middleware/
  web/                    # serves the existing frontend/ build output
  test/
    contract/             # golden-file tests vs recorded C# responses
    integration/          # end-to-end against ephemeral SQLite + Postgres
  analysis/               # Phase 0.5 deliverables — behavior cards, schema dumps, inventories
```

## Prerequisites

- Go 1.26+
- `golangci-lint` 2.x
- For Phase 0.5 dynamic capture: an existing C# Sonarr build (`dotnet 10`, the same version `src/` targets).

## Quickstart

```sh
cd go
go mod tidy
go vet ./...
golangci-lint run ./...
go test ./...
go build ./cmd/sonarr
```

## Phase status

See `../​[plan file]` for the current phase. PRs must cite which Phase 0.5 analysis card they port from.
