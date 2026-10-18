// Package playback inventories compressed recordings and serves virtual MP4
// fragments. Preparation belongs to the parked worker; serving never scans or
// invokes an encoder, probe or external process.
package playback

import (
	"context"
	"errors"
	"io"
)

const (
	Version          = 1
	Timescale        = 90000
	FrameTicks       = Timescale / 20
	FragmentTicks    = 2 * Timescale
	MaxSourceBytes   = 256 << 20
	MaxNALBytes      = 4 << 20
	MaxSamples       = 12000
	MaxAudioSamples  = 32000
	MaxSpans         = 60000
	MaxIndexBytes    = 4 << 20
	MaxFragmentBytes = 32 << 20
	CopyBufferBytes  = 64 << 10
)

var ErrUnsupported = errors.New("recording is not supported for native HEVC playback")

type Span struct {
	Offset int64
	Size   uint32
}
type Sample struct {
	Time     uint64
	Duration uint32
	Key      bool
	Spans    []Span
}
type Fragment struct {
	First, End      int
	Start, Duration uint64
}
type Video struct {
	Parameters    [3][]byte
	Codec         string
	Width, Height uint32
	Samples       []Sample
	Fragments     []Fragment
	// OriginNS maps media time zero to the recorded camera exposure clock.
	OriginNS uint64
}
type Audio struct {
	// Silence keeps the session's audio track stable through entirely silent
	// parts. It is a request-time overlay, never a claim of recorded sound.
	Silence          bool
	Object, Channels byte
	Rate             uint32
	Samples          []Sample
	// VideoPTS is the original transport stream video origin in 90 kHz ticks.
	VideoPTS int64
}
type Index struct {
	Version              int
	Video                Video
	Audio                Audio
	VideoSize, AudioSize int64
}
type FrameTime struct {
	Index uint32
	EndNS uint64
}

// Reader makes cancellation apply even while inventorying damaged sources.
type Reader struct {
	Context context.Context
	Source  io.Reader
}

func (r Reader) Read(p []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	return r.Source.Read(p)
}
