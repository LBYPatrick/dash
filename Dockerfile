# Development sandbox only. Comma installations use systemd and native binaries.
FROM debian:bookworm-slim AS media
COPY --from=golang:1.27.1-bookworm /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get update && apt-get install -y --no-install-recommends ffmpeg bzip2 && rm -rf /var/lib/apt/lists/*

# Use the official Vite+ builder; its media libraries stay in this stage.
# The Go/runtime stages continue sharing the separate Debian media libraries.
FROM ghcr.io/voidzero-dev/vite-plus:1.2.0 AS web
USER root
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg bzip2 && rm -rf /var/lib/apt/lists/* && mkdir -p /work && chown vp:vp /work
USER vp
WORKDIR /work
COPY --chown=vp:vp package.json pnpm-lock.yaml pnpm-workspace.yaml .node-version ./
COPY --chown=vp:vp frontend/package.json ./frontend/
RUN vp install --frozen-lockfile
COPY --chown=vp:vp vite.config.ts ./
COPY --chown=vp:vp frontend ./frontend
COPY --chown=vp:vp .github/workflows/channels.yml ./.github/workflows/channels.yml
COPY --chown=vp:vp scripts ./scripts
COPY --chown=vp:vp backend/internal/telemetry/testdata ./backend/internal/telemetry/testdata
RUN vp test run && vp -C frontend run build

FROM golang:1.27.1-bookworm AS backend
# All stages use Debian bookworm. Reuse the exact FFmpeg binaries and shared
# libraries from one media dependency stage rather than downloading them twice.
COPY --from=media /usr/bin/ffmpeg /usr/bin/ffprobe /usr/bin/
COPY --from=media /usr/lib/ /usr/lib/
COPY --from=media /etc/alternatives/ /etc/alternatives/
WORKDIR /work/backend
COPY backend/go.mod backend/go.sum ./
COPY --from=gomodcache / /go/pkg/mod/cache/download/
RUN go mod download
COPY backend ./
COPY frontend/public /work/frontend/public
COPY scripts /work/scripts
COPY Makefile package.json /work/
COPY .github /work/.github
RUN go vet ./... && go test -race ./... && CGO_ENABLED=0 go build -trimpath -o /dash-backend .
RUN go run ./cmd/dash-tool fixture

FROM debian:bookworm-slim AS sandbox
COPY --from=media /usr/bin/ffmpeg /usr/bin/ffprobe /usr/bin/
COPY --from=media /usr/lib/ /usr/lib/
COPY --from=media /etc/alternatives/ /etc/alternatives/
COPY --from=media /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN useradd --uid 1000 --create-home dash \
    && mkdir -p /data/dash/cache /data/params/d \
    && chown -R dash:dash /data
COPY --from=backend /dash-backend /usr/local/bin/dash-backend
COPY --from=web /work/frontend/dist /app
COPY --from=backend /work/.artifacts/device/recordings /recordings
COPY docker/entrypoint.sh /usr/local/bin/dash-sandbox
USER dash
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/dash-sandbox"]
