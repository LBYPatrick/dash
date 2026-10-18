package playback

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// EncodeIndex is a typed, versioned, checksummed compressed metadata file. It
// contains byte spans and timestamps only, never copied camera/audio payload.
func EncodeIndex(index *Index) ([]byte, error) {
	if err := Validate(index); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxIndexBytes {
		return nil, fmt.Errorf("native index exceeds budget")
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20), zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	digest := sha256.Sum256(raw)
	return append(digest[:], encoder.EncodeAll(raw, nil)...), nil
}

// DecodeIndex verifies the compressed inventory before using any source spans.
func DecodeIndex(data []byte) (*Index, error) {
	if len(data) < 32 || len(data) > MaxIndexBytes {
		return nil, fmt.Errorf("invalid native index size")
	}
	decoder, err := zstd.NewReader(bytes.NewReader(data[32:]), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(16<<20))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	raw, err := io.ReadAll(io.LimitReader(decoder, MaxIndexBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxIndexBytes {
		return nil, fmt.Errorf("native index exceeds decoded budget")
	}
	digest := sha256.Sum256(raw)
	if !bytes.Equal(digest[:], data[:32]) {
		return nil, fmt.Errorf("native index checksum mismatch")
	}
	var index Index
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, err
	}
	if err := Validate(&index); err != nil {
		return nil, err
	}
	return &index, nil
}

// Validate enforces sample, time, source-range and fragment memory budgets.
func Validate(index *Index) error {
	if index == nil || index.Version != Version || index.VideoSize <= 0 || index.VideoSize > MaxSourceBytes || index.AudioSize < 0 || index.AudioSize > MaxSourceBytes {
		return fmt.Errorf("invalid native source identity")
	}
	v := &index.Video
	if len(v.Samples) == 0 || len(v.Samples) > MaxSamples || len(v.Fragments) == 0 || len(v.Fragments) > MaxSamples || v.Width == 0 || v.Height == 0 || v.Width > 8192 || v.Height > 8192 || v.Codec == "" || len(v.Codec) > 128 {
		return fmt.Errorf("invalid native video inventory")
	}
	for _, p := range v.Parameters {
		if len(p) < 3 || len(p) > 64<<10 {
			return fmt.Errorf("invalid HEVC parameters")
		}
	}
	count := 0
	validate := func(samples []Sample, size int64, video bool) error {
		previous := uint64(0)
		for n, sample := range samples {
			if sample.Duration == 0 || sample.Time > 600*uint64(max(uint32(Timescale), index.Audio.Rate)) || (n > 0 && sample.Time <= previous) {
				return fmt.Errorf("invalid native sample time")
			}
			previous = sample.Time
			if video && (sample.Duration > 10*Timescale || (n == 0 && sample.Time != 0)) {
				return fmt.Errorf("invalid HEVC presentation clock")
			}
			if video && n+1 < len(samples) && sample.Time+uint64(sample.Duration) != samples[n+1].Time {
				return fmt.Errorf("HEVC sample durations do not match timestamps")
			}
			if video && len(sample.Spans) == 0 {
				return fmt.Errorf("missing HEVC source spans")
			}
			if len(sample.Spans) > 64 {
				return fmt.Errorf("sample span limit")
			}
			count += len(sample.Spans)
			if count > MaxSpans {
				return fmt.Errorf("native source span limit")
			}
			bytes := uint64(0)
			for _, span := range sample.Spans {
				if span.Offset < 0 || span.Size == 0 || int64(span.Size) > size || span.Offset > size-int64(span.Size) {
					return fmt.Errorf("invalid native source span")
				}
				bytes += uint64(span.Size)
				if video && span.Size > MaxNALBytes {
					return fmt.Errorf("HEVC NAL exceeds budget")
				}
			}
			if (video && bytes > 8<<20) || (!video && bytes > 8192) {
				return fmt.Errorf("native sample exceeds budget")
			}
		}
		return nil
	}
	if err := validate(v.Samples, index.VideoSize, true); err != nil {
		return err
	}
	end := 0
	for _, f := range v.Fragments {
		if f.First != end || f.End <= f.First || f.End > len(v.Samples) || !v.Samples[f.First].Key || f.Start != v.Samples[f.First].Time || f.Duration == 0 {
			return fmt.Errorf("invalid native fragment plan")
		}
		last := v.Samples[f.End-1]
		if f.Start+f.Duration != last.Time+uint64(last.Duration) {
			return fmt.Errorf("inconsistent native fragment duration")
		}
		bytes := uint64(0)
		for _, sample := range v.Samples[f.First:f.End] {
			for _, span := range sample.Spans {
				bytes += uint64(span.Size) + 4
			}
		}
		if bytes > MaxFragmentBytes {
			return fmt.Errorf("HEVC fragment exceeds budget")
		}
		end = f.End
	}
	if end != len(v.Samples) {
		return fmt.Errorf("incomplete native fragment inventory")
	}
	if len(index.Audio.Samples) > MaxAudioSamples {
		return fmt.Errorf("native AAC sample limit")
	}
	if len(index.Audio.Samples) > 0 || index.Audio.Silence {
		if index.Audio.Rate < 7350 || index.Audio.Rate > 96000 || index.Audio.Object != 2 || index.Audio.Channels < 1 || index.Audio.Channels > 2 {
			return fmt.Errorf("invalid native AAC configuration")
		}
		return validate(index.Audio.Samples, index.AudioSize, false)
	}
	return nil
}
