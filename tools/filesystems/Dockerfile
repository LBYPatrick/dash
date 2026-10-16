# Build hosts only. Devices receive static helpers, never packages or Docker.
FROM alpine:3.23@sha256:85fe1e81d6758c208f3e1eed4338a1997e19d4be002d4dd32d3100c9a8c010a0 AS builder
RUN apk add --no-cache build-base linux-headers pkgconf meson ninja autoconf automake libtool gettext-dev libgcrypt-dev patch
COPY --from=sources /fuse.tar.gz /source/fuse.tar.gz
COPY --from=sources /exfat.tar.gz /source/exfat.tar.gz
COPY --from=sources /ntfs.tar.gz /source/ntfs.tar.gz
COPY tools/filesystems/build.sh /source/build.sh
COPY tools/filesystems/exfat-exclusive.patch /source/exfat-exclusive.patch
RUN sh /source/build.sh
FROM scratch AS bundle
COPY --from=builder /out/ /
