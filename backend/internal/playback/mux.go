package playback

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"github.com/Eyevinn/mp4ff/mp4"
)

// CompleteAudio pads clock gaps with constant AAC-LC silence only when real
// recorded samples exist. It does not create a track for an empty microphone.
// The bytes encode zero spectral coefficients; no encoder runs during playback.
func CompleteAudio(audio Audio, duration uint64, shiftNS int64) Audio {
	if len(audio.Samples) == 0 {
		return Audio{}
	}
	var result []Sample
	addedReal := false
	next := uint64(0)
	limit := duration * uint64(audio.Rate) / Timescale
	shift := shiftNS * int64(audio.Rate) / 1e9
	silence := func(end uint64) {
		for next < end && len(result) < MaxAudioSamples {
			n := min(uint64(1024), end-next)
			result = append(result, Sample{Time: next, Duration: uint32(n)})
			next += n
		}
	}
	for _, sample := range audio.Samples {
		timestamp := int64(sample.Time) + shift
		if timestamp < 0 || uint64(timestamp) >= limit {
			continue
		}
		sample.Time = uint64(timestamp)
		if sample.Time > next+2 {
			silence(sample.Time)
		}
		if sample.Time+1024 <= next {
			continue
		}
		// Timestamp rounding between 90 kHz PES and the AAC sample clock may differ
		// by one sample. Keep the original PTS rather than accumulate that error.
		if len(result) > 0 {
			previous := &result[len(result)-1]
			if sample.Time > previous.Time {
				previous.Duration = uint32(sample.Time - previous.Time)
			}
		}
		result = append(result, sample)
		addedReal = true
		next = sample.Time + uint64(sample.Duration)
	}
	if !addedReal {
		return Audio{}
	}
	if next < limit {
		silence(limit)
	}
	audio.Samples = result
	return audio
}
func silentAAC(channels byte) []byte {
	if channels == 1 {
		return []byte{0x00, 0xc8, 0x00, 0x80, 0x23, 0x80}
	}
	return []byte{0x21, 0x10, 0x04, 0x60, 0x8c, 0x1c}
}

// Init creates a small hvc1 initialization segment with an optional AAC track.
func Init(index *Index) ([]byte, error) {
	init := mp4.CreateEmptyInit()
	track := init.AddEmptyTrack(Timescale, "video", "und")
	p := index.Video.Parameters
	if err := track.SetHEVCDescriptor("hvc1", [][]byte{p[0]}, [][]byte{p[1]}, [][]byte{p[2]}, nil, true); err != nil {
		return nil, err
	}
	if len(index.Audio.Samples) > 0 || index.Audio.Silence {
		track = init.AddEmptyTrack(index.Audio.Rate, "audio", "und")
		if err := track.SetAACDescriptorWithChannelConfig(index.Audio.Object, index.Audio.Channels, int(index.Audio.Rate)); err != nil {
			return nil, err
		}
	}
	var buffer bytes.Buffer
	err := init.Encode(&buffer)
	return buffer.Bytes(), err
}

type piece struct {
	data  []byte
	span  Span
	audio bool
}

// FragmentReader is a seekable virtual file. Its only buffers are MP4 headers,
// NAL lengths and a small index; encoded pictures are read from their originals.
type FragmentReader struct {
	ctx            context.Context
	video, audio   io.ReaderAt
	pieces         []piece
	starts         []int64
	size, position int64
}

// NewFragment plans bounded headers and source spans without copying media.
func NewFragment(ctx context.Context, index *Index, number int, video, audio io.ReaderAt) (*FragmentReader, error) {
	if err := Validate(index); err != nil {
		return nil, err
	}
	if number < 0 || number >= len(index.Video.Fragments) {
		return nil, fmt.Errorf("invalid fragment number")
	}
	plan := index.Video.Fragments[number]
	if video == nil {
		return nil, fmt.Errorf("missing video reader")
	}
	reader := &FragmentReader{ctx: ctx, video: video, audio: audio}
	tracks := []uint32{1}
	if len(index.Audio.Samples) > 0 || index.Audio.Silence {
		tracks = append(tracks, 2)
	}
	fragment, err := mp4.CreateMultiTrackFragment(uint32(number+1), tracks)
	if err != nil {
		return nil, err
	}
	var payload []piece
	for _, sample := range index.Video.Samples[plan.First:plan.End] {
		size := uint32(0)
		for _, span := range sample.Spans {
			size += 4 + span.Size
			prefix := make([]byte, 4)
			binary.BigEndian.PutUint32(prefix, span.Size)
			payload = append(payload, piece{data: prefix}, piece{span: span})
		}
		flags := mp4.NonSyncSampleFlags | mp4.SampleDependsOn1
		if sample.Key {
			flags = mp4.SyncSampleFlags
		}
		if err := fragment.AddSampleToTrack(mp4.Sample{Flags: flags, Dur: sample.Duration, Size: size}, 1, sample.Time); err != nil {
			return nil, err
		}
	}
	if len(index.Audio.Samples) > 0 || index.Audio.Silence {
		start := plan.Start * uint64(index.Audio.Rate) / Timescale
		end := (plan.Start + plan.Duration) * uint64(index.Audio.Rate) / Timescale
		samples := index.Audio.Samples
		if index.Audio.Silence {
			for at := (start + 1023) / 1024 * 1024; at < end; at += 1024 {
				samples = append(samples, Sample{Time: at, Duration: 1024})
			}
		}
		first := sort.Search(len(samples), func(n int) bool { return samples[n].Time >= start })
		last := sort.Search(len(samples), func(n int) bool { return samples[n].Time >= end })
		for _, sample := range samples[first:last] {
			size := uint32(0)
			if len(sample.Spans) == 0 {
				data := silentAAC(index.Audio.Channels)
				size = uint32(len(data))
				payload = append(payload, piece{data: data})
			} else {
				if audio == nil {
					return nil, fmt.Errorf("missing AAC source")
				}
				for _, span := range sample.Spans {
					size += span.Size
					payload = append(payload, piece{span: span, audio: true})
				}
			}
			if err := fragment.AddSampleToTrack(mp4.Sample{Flags: mp4.SyncSampleFlags, Dur: sample.Duration, Size: size}, 2, sample.Time); err != nil {
				return nil, err
			}
		}
	}
	if fragment.Mdat.GetLazyDataSize() > MaxFragmentBytes {
		return nil, fmt.Errorf("native fragment exceeds 32 MiB")
	}
	var header bytes.Buffer
	if err := fragment.Encode(&header); err != nil {
		return nil, err
	}
	reader.pieces = append([]piece{{data: header.Bytes()}}, payload...)
	for _, piece := range reader.pieces {
		reader.starts = append(reader.starts, reader.size)
		if piece.data != nil {
			reader.size += int64(len(piece.data))
		} else {
			reader.size += int64(piece.span.Size)
		}
	}
	return reader, nil
}
func (r *FragmentReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.position >= r.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(data) && r.position < r.size {
		if err := r.ctx.Err(); err != nil {
			return n, err
		}
		at := sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > r.position }) - 1
		piece := r.pieces[at]
		relative := r.position - r.starts[at]
		size := int64(len(piece.data))
		if piece.data == nil {
			size = int64(piece.span.Size)
		}
		count := int(min(int64(len(data)-n), size-relative, int64(CopyBufferBytes)))
		if piece.data != nil {
			copy(data[n:n+count], piece.data[relative:relative+int64(count)])
		} else {
			source := r.video
			if piece.audio {
				source = r.audio
			}
			got, err := source.ReadAt(data[n:n+count], piece.span.Offset+relative)
			r.position += int64(got)
			n += got
			if err != nil {
				return n, err
			}
			if got != count {
				return n, io.ErrUnexpectedEOF
			}
			continue
		}
		r.position += int64(count)
		n += count
	}
	return n, nil
}
func (r *FragmentReader) Seek(offset int64, whence int) (int64, error) {
	target := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		target += r.position
	case io.SeekEnd:
		target += r.size
	default:
		return 0, fmt.Errorf("invalid seek mode")
	}
	if target < 0 {
		return r.position, fmt.Errorf("negative fragment seek")
	}
	r.position = target
	return target, nil
}
