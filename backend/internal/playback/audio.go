package playback

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// InventoryAudio reads fixed-size MPEG-TS packets. Only the bounded current AAC
// PES is buffered. Source spans omit TS/PES/ADTS headers in the virtual MP4.
func InventoryAudio(ctx context.Context, source io.Reader) (Audio, error) {
	source = bufio.NewReaderSize(Reader{ctx, source}, CopyBufferBytes)
	var audio Audio
	var packet [188]byte
	pmtPID, audioPID, videoPID := uint16(0xffff), uint16(0xffff), uint16(0xffff)
	var pes []byte
	var spans []Span
	var pesPTS int64
	var expected int
	totalSpans := 0
	var videoPTS int64
	videoFound := false
	offset := int64(0)
	lastCC := -1
	discard := false
	flush := func() error {
		if len(pes) == 0 {
			return nil
		}
		if len(pes) < 9 || pes[0] != 0 || pes[1] != 0 || pes[2] != 1 {
			return fmt.Errorf("invalid AAC PES")
		}
		header := 9 + int(pes[8])
		if header > len(pes) {
			return fmt.Errorf("truncated AAC PES header")
		}
		end := len(pes)
		if expected > 0 {
			if expected > end {
				return nil
			}
			end = expected
		}
		pts, ok := pesTimestamp(pes)
		if !ok {
			return fmt.Errorf("AAC PES lacks presentation timestamp")
		}
		pesPTS = pts
		at := header
		number := uint64(0)
		for at < end {
			if end-at < 7 {
				return fmt.Errorf("truncated AAC frame")
			}
			b := pes[at:]
			if b[0] != 0xff || b[1]&0xf6 != 0xf0 {
				return fmt.Errorf("unsupported AAC transport")
			}
			object := byte((b[2] >> 6) + 1)
			rateIndex := (b[2] >> 2) & 15
			rates := [13]uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
			channels := ((b[2] & 1) << 2) | (b[3] >> 6)
			if rateIndex >= 13 || object != 2 || channels < 1 || channels > 2 || b[6]&3 != 0 {
				return fmt.Errorf("unsupported AAC configuration")
			}
			rate := rates[rateIndex]
			length := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
			adts := 7
			if b[1]&1 == 0 {
				adts = 9
			}
			if length <= adts || at+length > end {
				return fmt.Errorf("truncated AAC payload")
			}
			if audio.Rate != 0 && (audio.Rate != rate || audio.Channels != channels || audio.Object != object) {
				return fmt.Errorf("AAC parameters change within part")
			}
			audio.Rate, audio.Channels, audio.Object = rate, channels, object
			if len(audio.Samples) >= MaxAudioSamples {
				return fmt.Errorf("AAC sample limit")
			}
			sample := Sample{Time: uint64(pesPTS)*uint64(rate)/Timescale + number*1024, Duration: 1024, Spans: subspans(spans, at+adts, length-adts)}
			totalSpans += len(sample.Spans)
			if totalSpans > MaxSpans {
				return fmt.Errorf("AAC source-span budget exceeded")
			}
			audio.Samples = append(audio.Samples, sample)
			number++
			at += length
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return audio, err
		}
		_, err := io.ReadFull(source, packet[:])
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			// ACC power can disappear in the middle of the final TS packet.
			// Keep complete earlier PES packets instead of losing all audio.
			break
		}
		if err != nil {
			return audio, fmt.Errorf("truncated TS packet: %w", err)
		}
		if packet[0] != 0x47 || packet[1]&0x80 != 0 || packet[3]&0xc0 != 0 {
			return audio, fmt.Errorf("invalid MPEG-TS packet")
		}
		pid := uint16(packet[1]&31)<<8 | uint16(packet[2])
		pusi := packet[1]&0x40 != 0
		payload := 4
		adaptation := (packet[3] >> 4) & 3
		if adaptation == 0 {
			return audio, fmt.Errorf("invalid TS adaptation control")
		}
		if adaptation&2 != 0 {
			payload += 1 + int(packet[4])
		}
		if payload > 188 {
			return audio, fmt.Errorf("invalid TS adaptation length")
		}
		if adaptation&1 == 0 || payload == 188 {
			offset += 188
			continue
		}
		data := packet[payload:]
		if pusi && (pid == 0 || pid == pmtPID) {
			start := 1 + int(data[0])
			if start+3 <= len(data) {
				section := data[start:]
				size := 3 + (int(section[1]&15)<<8 | int(section[2]))
				if size <= len(section) && size >= 12 {
					section = section[:size]
					if pid == 0 && section[0] == 0 {
						for at := 8; at+4 <= len(section)-4; at += 4 {
							if section[at] != 0 || section[at+1] != 0 {
								pmtPID = uint16(section[at+2]&31)<<8 | uint16(section[at+3])
								break
							}
						}
					}
					if pid == pmtPID && section[0] == 2 && len(section) >= 16 {
						at := 12 + (int(section[10]&15)<<8 | int(section[11]))
						for at+5 <= len(section)-4 {
							kind := section[at]
							elementPID := uint16(section[at+1]&31)<<8 | uint16(section[at+2])
							if kind == 0x0f {
								audioPID = elementPID
							}
							if kind == 0x1b || kind == 0x24 {
								videoPID = elementPID
							}
							at += 5 + (int(section[at+3]&15)<<8 | int(section[at+4]))
						}
					}
				}
			}
		}
		if pid == videoPID && pusi && !videoFound {
			if pts, ok := pesTimestamp(data); ok {
				videoPTS = pts
				videoFound = true
			}
		}
		if pid == audioPID {
			cc := int(packet[3] & 15)
			if lastCC >= 0 && cc == lastCC {
				offset += 188
				continue
			} // repeated packet
			if lastCC >= 0 && cc != (lastCC+1)&15 {
				pes = nil
				spans = nil
				discard = true
			}
			lastCC = cc
			if pusi {
				if !discard {
					if err := flush(); err != nil {
						return audio, err
					}
				}
				pes = nil
				spans = nil
				expected = 0
				discard = false
				if len(data) >= 6 {
					length := int(data[4])<<8 | int(data[5])
					if length > 0 {
						expected = 6 + length
					}
				}
			}
			if !discard {
				if len(pes)+len(data) > 256<<10 {
					return audio, fmt.Errorf("AAC PES exceeds limit")
				}
				pes = append(pes, data...)
				spans = append(spans, Span{offset + int64(payload), uint32(len(data))})
			}
		}
		offset += 188
	}
	if !discard {
		if err := flush(); err != nil {
			return audio, err
		}
	}
	if len(audio.Samples) == 0 {
		return Audio{}, nil
	}
	if !videoFound {
		return audio, fmt.Errorf("AAC has no companion video clock")
	}
	audio.VideoPTS = videoPTS
	origin := uint64(videoPTS) * uint64(audio.Rate) / Timescale
	samples := audio.Samples[:0]
	for _, sample := range audio.Samples {
		// Normalize the 33-bit transport clock, including a wrap at part start.
		wrap := (uint64(1) << 33) * uint64(audio.Rate) / Timescale
		if sample.Time+wrap/2 < origin {
			sample.Time += wrap
		}
		if origin+wrap/2 < sample.Time {
			if sample.Time < wrap {
				continue
			}
			sample.Time -= wrap
		}
		if sample.Time < origin {
			continue
		}
		sample.Time -= origin
		if sample.Time > 600*uint64(audio.Rate) {
			return audio, fmt.Errorf("AAC clock exceeds part budget")
		}
		if len(samples) > 0 && sample.Time <= samples[len(samples)-1].Time {
			return audio, fmt.Errorf("nonmonotonic AAC clock")
		}
		samples = append(samples, sample)
	}
	audio.Samples = samples
	return audio, nil
}
func pesTimestamp(data []byte) (int64, bool) {
	if len(data) < 14 || data[0] != 0 || data[1] != 0 || data[2] != 1 || data[7]&0x80 == 0 || data[8] < 5 {
		return 0, false
	}
	b := data[9:14]
	if b[0]&1 == 0 || b[2]&1 == 0 || b[4]&1 == 0 {
		return 0, false
	}
	pts := int64(b[0]&14)<<29 | int64(b[1])<<22 | int64(b[2]&254)<<14 | int64(b[3])<<7 | int64(b[4]>>1)
	return pts, true
}
func subspans(spans []Span, start, size int) []Span {
	var result []Span
	for _, span := range spans {
		if start >= int(span.Size) {
			start -= int(span.Size)
			continue
		}
		n := min(size, int(span.Size)-start)
		result = append(result, Span{span.Offset + int64(start), uint32(n)})
		size -= n
		start = 0
		if size == 0 {
			break
		}
	}
	return result
}
