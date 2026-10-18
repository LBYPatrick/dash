package playback

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/Eyevinn/mp4ff/hevc"
)

// InventoryHEVC visits NAL units in original decode order, including streams
// without AUDs and pictures with multiple slices. It stores offsets, not video.
func InventoryHEVC(ctx context.Context, source io.Reader, frames []FrameTime) (Video, error) {
	var video Video
	scanner := bufio.NewScanner(Reader{ctx, source})
	scanner.Buffer(make([]byte, 64<<10), MaxNALBytes)
	scanner.Split(splitNAL)
	offset := int64(0)
	var current Sample
	hasPicture := false
	spans := 0
	flush := func() error {
		if !hasPicture {
			return nil
		}
		if len(video.Samples) >= MaxSamples {
			return fmt.Errorf("native HEVC sample limit")
		}
		if len(video.Samples) == 0 && !current.Key {
			return fmt.Errorf("%w: recording starts before an independent picture", ErrUnsupported)
		}
		current.Time = uint64(len(video.Samples)) * FrameTicks
		current.Duration = FrameTicks
		video.Samples = append(video.Samples, current)
		current = Sample{}
		hasPicture = false
		return nil
	}
	for scanner.Scan() {
		token := scanner.Bytes()
		if offset+int64(len(token)) > MaxSourceBytes {
			return video, fmt.Errorf("native HEVC source exceeds budget")
		}
		_, prefix := nalStart(token, 0)
		nal := bytes.TrimRight(token[prefix:], "\x00")
		if len(nal) < 3 || nal[0]&128 != 0 || nal[1]&7 == 0 || (nal[0]&1) != 0 || nal[1]>>3 != 0 {
			return video, fmt.Errorf("%w: invalid HEVC NAL header", ErrUnsupported)
		}
		kind := (nal[0] >> 1) & 63
		// A parameter set, AUD or prefix SEI after VCL begins the next AU.
		if hasPicture && ((kind >= 32 && kind <= 35) || kind == 39) {
			if err := flush(); err != nil {
				return video, err
			}
		}
		if kind >= 32 && kind <= 34 {
			number := kind - 32
			if len(nal) > 64<<10 {
				return video, fmt.Errorf("HEVC parameters exceed limit")
			}
			if len(video.Parameters[number]) > 0 && !bytes.Equal(video.Parameters[number], nal) {
				return video, fmt.Errorf("%w: decoder parameters change within a part", ErrUnsupported)
			}
			video.Parameters[number] = bytes.Clone(nal)
		} else if kind <= 31 {
			if kind > 9 && (kind < 16 || kind > 21) {
				return video, fmt.Errorf("%w: reserved HEVC picture", ErrUnsupported)
			}
			first := nal[2]&128 != 0
			if first && hasPicture {
				if err := flush(); err != nil {
					return video, err
				}
			}
			if !hasPicture && !first {
				return video, fmt.Errorf("%w: incomplete first picture", ErrUnsupported)
			}
			if !hasPicture {
				current.Key = kind >= 16 && kind <= 21
				hasPicture = true
			}
			current.Spans = append(current.Spans, Span{offset + int64(prefix), uint32(len(nal))})
		} else if kind == 39 || kind == 40 {
			current.Spans = append(current.Spans, Span{offset + int64(prefix), uint32(len(nal))})
		} else if kind != 35 && kind != 36 && kind != 37 && kind != 38 {
			return video, fmt.Errorf("%w: unsupported HEVC unit", ErrUnsupported)
		}
		if len(current.Spans) > 64 {
			return video, fmt.Errorf("HEVC slice count exceeds limit")
		}
		if kind <= 31 || kind == 39 || kind == 40 {
			spans++
			if spans > MaxSpans {
				return video, fmt.Errorf("HEVC source-span budget exceeded")
			}
		}
		offset += int64(len(token))
	}
	if err := scanner.Err(); err != nil {
		return video, err
	}
	if err := flush(); err != nil {
		return video, err
	}
	if len(video.Samples) == 0 {
		return video, fmt.Errorf("%w: no complete HEVC pictures", ErrUnsupported)
	}
	for _, parameter := range video.Parameters {
		if len(parameter) == 0 {
			return video, fmt.Errorf("%w: missing HEVC parameters", ErrUnsupported)
		}
	}
	sps, err := hevc.ParseSPSNALUnit(video.Parameters[1])
	if err != nil {
		return video, fmt.Errorf("%w: invalid SPS: %v", ErrUnsupported, err)
	}
	for _, ordering := range sps.SubLayeringOrderingInfos {
		if ordering.MaxNumReorderPics != 0 {
			return video, fmt.Errorf("%w: reordered HEVC pictures require a presentation-order index", ErrUnsupported)
		}
	}
	width, height := sps.ImageSize()
	video.Width, video.Height = uint32(width), uint32(height)
	if width == 0 || height == 0 || width > 8192 || height > 8192 {
		return video, fmt.Errorf("unsupported HEVC dimensions")
	}
	video.Codec = hevc.CodecString("hvc1", sps)
	// Logs are optional. Use them only when every stored picture has a matching,
	// monotonic exposure timestamp in encode order. Never sort encoded payload.
	if len(frames) == len(video.Samples) {
		sort.Slice(frames, func(a, b int) bool { return frames[a].Index < frames[b].Index })
		valid := frames[0].EndNS > 0
		for n := 1; n < len(frames); n++ {
			if frames[n].Index != frames[n-1].Index+1 || frames[n].EndNS <= frames[n-1].EndNS || frames[n].EndNS-frames[n-1].EndNS > 10e9 {
				valid = false
			}
		}
		if valid {
			video.OriginNS = frames[0].EndNS
			for n := range video.Samples {
				video.Samples[n].Time = (frames[n].EndNS - video.OriginNS) * Timescale / 1e9
			}
			for n := 0; n < len(video.Samples)-1; n++ {
				video.Samples[n].Duration = uint32(video.Samples[n+1].Time - video.Samples[n].Time)
			}
		}
	}
	first := 0
	for n := 1; n < len(video.Samples); n++ {
		if video.Samples[n].Key && video.Samples[n].Time-video.Samples[first].Time >= FragmentTicks {
			video.Fragments = append(video.Fragments, Fragment{first, n, video.Samples[first].Time, video.Samples[n].Time - video.Samples[first].Time})
			first = n
		}
	}
	last := video.Samples[len(video.Samples)-1]
	video.Fragments = append(video.Fragments, Fragment{first, len(video.Samples), video.Samples[first].Time, last.Time + uint64(last.Duration) - video.Samples[first].Time})
	return video, nil
}
func nalStart(data []byte, from int) (int, int) {
	at := bytes.Index(data[from:], []byte{0, 0, 1})
	if at < 0 {
		return -1, 0
	}
	at += from
	if at > 0 && data[at-1] == 0 {
		return at - 1, 4
	}
	return at, 3
}
func splitNAL(data []byte, eof bool) (int, []byte, error) {
	at, prefix := nalStart(data, 0)
	if at < 0 {
		if eof && len(data) > 0 {
			return 0, nil, fmt.Errorf("missing HEVC start code")
		}
		return 0, nil, nil
	}
	if at != 0 {
		return 0, nil, fmt.Errorf("invalid HEVC prefix")
	}
	next, _ := nalStart(data, prefix)
	if next >= 0 {
		return next, data[:next], nil
	}
	if eof {
		return len(data), data, nil
	}
	return 0, nil, nil
}
