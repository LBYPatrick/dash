# Development sandbox only. Comma installations use systemd and native binaries.
FROM node:22-bookworm-slim AS media
COPY --from=golang:1.27.1-bookworm /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get update && apt-get install -y --no-install-recommends ffmpeg bzip2 && rm -rf /var/lib/apt/lists/*

FROM media AS web
WORKDIR /work
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
COPY public ./public
COPY scripts/prepare-fixture.mjs scripts/fixture-clock.mjs ./scripts/
COPY dash-backend/internal/telemetry/testdata ./dash-backend/internal/telemetry/testdata
RUN npm test && npm run build && node scripts/prepare-fixture.mjs

FROM golang:1.27.1-bookworm AS backend
# All stages use Debian bookworm. Reuse the exact FFmpeg binaries and shared
# libraries from one media dependency stage rather than downloading them twice.
COPY --from=media /usr/bin/ffmpeg /usr/bin/ffprobe /usr/bin/
COPY --from=media /usr/lib/ /usr/lib/
COPY --from=media /etc/alternatives/ /etc/alternatives/
WORKDIR /work/dash-backend
COPY dash-backend/go.mod dash-backend/go.sum ./
COPY --from=gomodcache / /go/pkg/mod/cache/download/
RUN go mod download
COPY dash-backend ./
COPY public /work/public
RUN go vet ./... && go test -race ./... && CGO_ENABLED=0 go build -trimpath -o /dash-backend .

FROM debian:bookworm-slim AS sandbox
COPY --from=media /usr/bin/ffmpeg /usr/bin/ffprobe /usr/bin/
COPY --from=media /usr/lib/ /usr/lib/
COPY --from=media /etc/alternatives/ /etc/alternatives/
COPY --from=media /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN useradd --uid 1000 --create-home dash \
    && mkdir -p /data/dash/cache /data/params/d \
    && chown -R dash:dash /data
COPY --from=backend /dash-backend /usr/local/bin/dash-backend
COPY --from=web /work/dist /app
COPY --from=web /work/.artifacts/device/recordings /recordings
COPY docker/entrypoint.sh /usr/local/bin/dash-sandbox
USER dash
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/dash-sandbox"]
