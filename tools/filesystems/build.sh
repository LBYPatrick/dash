#!/bin/sh
set -eu
mkdir -p /work/fuse /work/exfat /work/ntfs /out/sources /out/licenses
cp /source/*.tar.gz /source/build.sh /source/exfat-exclusive.patch /out/sources/
for name in fuse exfat ntfs; do tar -xf "/source/$name.tar.gz" -C "/work/$name" --strip-components=1; done
cd /work/fuse
meson setup build --prefix=/opt/dash-fs --libdir=lib --default-library=static \
  -Dutils=false -Dexamples=false -Dtests=false -Duseroot=false \
  -Ddisable-mtab=true -Ddisable-libc-symbol-version=true -Denable-io-uring=false -Dinitscriptdir=''
ninja -C build -j2
ninja -C build install
cp LGPL2.txt /out/licenses/libfuse-LGPL-2.1.txt
cd /work/exfat
patch -p1 < /source/exfat-exclusive.patch
PKG_CONFIG_PATH=/opt/dash-fs/lib/pkgconfig PKG_CONFIG='pkgconf --static' LDFLAGS='-static' ./configure --prefix=/opt/dash-fs
make -j2
make install
cp /opt/dash-fs/sbin/mount.exfat-fuse /out/mount.exfat-fuse
cp COPYING /out/licenses/exfat-GPL-2.0.txt
cd /work/ntfs
autoreconf -fi
./configure --prefix=/opt/dash-fs --exec-prefix=/opt/dash-fs --disable-shared --enable-static --enable-really-static \
  --with-fuse=internal --disable-ntfsprogs --disable-mount-helper
make -j2
make install
cp /opt/dash-fs/bin/ntfs-3g /out/ntfs-3g
cp COPYING /out/licenses/ntfs-3g-GPL-2.0.txt
# Only mount helpers are distributed; no formatter, repair or partition tools.
strip /out/mount.exfat-fuse /out/ntfs-3g
