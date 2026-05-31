# Multi-stage build for the Go port of Sonarr.
#
#   web stage    -> builds the React UI (webpack) into _output/UI
#   build stage  -> embeds the UI into go/internal/frontend/bundle/ and
#                   compiles a static (CGO_ENABLED=0) linux binary
#   runtime stage-> slim Alpine image carrying ONLY the binary + ffprobe
#                   (ffmpeg), the single native runtime dependency.
#
# Build from the repository root:
#   docker build -f distribution/docker-go/Dockerfile -t sonarr-go:dev .
#
# Run:
#   docker run -d -p 8989:8989 -v $PWD/_data:/config sonarr-go:dev

# ---- web stage: build the React bundle ----------------------------------
FROM node:26-bookworm-slim AS web
WORKDIR /src
# yarn 1.x is no longer bundled with recent node images; install it pinned.
RUN npm install -g yarn@1.22.19
# Dependency install is cached on package.json + yarn.lock alone.
COPY package.json yarn.lock ./
RUN yarn install --frozen-lockfile
# Bring in the rest of the sources the webpack build reads, then build.
COPY . .
RUN yarn build && test -f _output/UI/index.html

# ---- build stage: compile the static Go binary -------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src/go
# Module cache layer (go.mod/go.sum only) for faster rebuilds.
COPY go/go.mod go/go.sum ./
RUN go mod download
# Application sources.
COPY go/ ./
# Replace the embedded placeholder bundle with the freshly-built UI so
# //go:embed all:bundle ships the real React app. The COPY creates the
# directory; .dockerignore keeps any locally-staged bundle out of context.
COPY --from=web /src/_output/UI/ ./internal/frontend/bundle/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /sonarr ./cmd/sonarr

# ---- runtime stage ------------------------------------------------------
FROM alpine:3.21 AS runtime
# ffmpeg supplies ffprobe (MediaInfo). ca-certificates for outbound HTTPS
# to indexers; tzdata for scheduled-task timezones.
RUN apk add --no-cache ffmpeg ca-certificates tzdata \
    && addgroup -S sonarr && adduser -S -G sonarr -H -h /config sonarr \
    && mkdir -p /config && chown sonarr:sonarr /config
COPY --from=build /sonarr /usr/local/bin/sonarr
USER sonarr
VOLUME /config
EXPOSE 8989
ENTRYPOINT ["/usr/local/bin/sonarr", "--data=/config", "--nobrowser"]
