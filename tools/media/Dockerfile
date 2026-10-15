# Build-host only. No containers or packages are installed on a comma device.
FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0 AS builder
RUN apk add --no-cache build-base linux-headers pkgconf nasm bash xz
COPY --from=sources /ffmpeg.tar.xz /source/ffmpeg.tar.xz
COPY --from=sources /x264.tar.gz /source/x264.tar.gz
COPY tools/media/build.sh /source/build.sh
RUN sh /source/build.sh
FROM scratch AS bundle
COPY --from=builder /out/ /
