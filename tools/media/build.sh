#!/bin/sh
set -eu
mkdir -p /work/x264 /work/ffmpeg /out/sources /out/licenses
cp /source/ffmpeg.tar.xz /source/x264.tar.gz /source/build.sh /out/sources/
tar -xf /source/x264.tar.gz -C /work/x264 --strip-components=1
tar -xf /source/ffmpeg.tar.xz -C /work/ffmpeg --strip-components=1
cd /work/x264
./configure --prefix=/opt/dash-media --enable-static --disable-shared --disable-cli --disable-opencl --enable-pic
make -j2
make install
cp COPYING /out/licenses/x264-GPL-2.0.txt
cd /work/ffmpeg
PKG_CONFIG_PATH=/opt/dash-media/lib/pkgconfig ./configure \
  --prefix=/opt/dash-media --pkg-config-flags=--static \
  --extra-cflags=-I/opt/dash-media/include --extra-ldflags=-L/opt/dash-media/lib \
  --extra-ldexeflags='-static -Wl,-z,stack-size=2097152' --enable-static --disable-shared \
  --enable-gpl --enable-libx264 --disable-autodetect --disable-network \
  --disable-doc --disable-debug --disable-everything \
  --enable-ffmpeg --enable-ffprobe --enable-avcodec --enable-avformat \
  --enable-avfilter --enable-avdevice --enable-swscale --enable-pthreads \
  --enable-decoder=h264,hevc,aac,wrapped_avframe --enable-parser=h264,hevc,aac \
  --enable-encoder=libx264,mjpeg,rawvideo,wrapped_avframe \
  --enable-demuxer=mov,mpegts,hevc,h264,concat --enable-indev=lavfi \
  --enable-muxer=mp4,mpegts,hevc,h264,image2,image2pipe,null \
  --enable-protocol=file,pipe \
  --enable-filter=scale,format,fps,setsar,setpts,null,color,testsrc2 \
  --enable-bsf=h264_mp4toannexb,hevc_mp4toannexb,h264_metadata,hevc_metadata,aac_adtstoasc,extract_extradata
make -j2
make install
cp /opt/dash-media/bin/ffmpeg /opt/dash-media/bin/ffprobe /out/
cp COPYING.GPLv2 COPYING.LGPLv2.1 LICENSE.md /out/licenses/
cp config.h config_components.h ffbuild/config.mak /out/sources/
