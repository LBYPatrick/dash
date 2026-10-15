package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
)

// Raw native HEVC has no container duration. Count first slices in Annex B
// instead of launching FFprobe and initializing its decoder for every camera.
// FFmpeg's HEVC frame-boundary parser uses the same first-slice flag. This is a
// packet inventory, not validation that a damaged frame can actually decode.
// Unfamiliar headers/layers fall back to FFprobe in videoDuration.
func rawHEVCDuration(ctx context.Context, filename string) (float64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return countHEVCPictures(ctx, file)
}
func countHEVCPictures(ctx context.Context, reader io.Reader) (float64, error) {
	scanner := bufio.NewScanner(checkedReader{ctx, reader})
	scanner.Buffer(make([]byte, 64<<10), maxVideoFrame)
	scanner.Split(splitIndexNAL)
	parameters := uint8(0)
	frames := 0
	key := false
	for scanner.Scan() {
		nal := scanner.Bytes()
		_, prefix := indexNALStart(nal, 0)
		if prefix == 0 || len(nal) < prefix+3 {
			return 0, errors.New("short HEVC header")
		}
		header := nal[prefix:]
		kind := (header[0] >> 1) & 63
		layer := ((header[0] & 1) << 5) | (header[1] >> 3)
		if header[0]&128 != 0 || header[1]&7 == 0 || layer != 0 {
			return 0, errors.New("unfamiliar HEVC layer/header")
		}
		switch {
		case kind >= 32 && kind <= 34:
			parameters |= 1 << (kind - 32)
		case kind <= 9 || (kind >= 16 && kind <= 21):
			if parameters != 7 {
				return 0, errors.New("HEVC lacks initial decoder parameters")
			}
			if header[2]&128 != 0 {
				frames++
				if kind >= 16 && kind <= 21 {
					key = true
				}
				if frames > maxVideoPackets {
					return 0, errors.New("HEVC exceeds frame inventory budget")
				}
			} else if frames == 0 {
				return 0, errors.New("HEVC begins in the middle of a picture")
			}
		case kind >= 35 && kind <= 40:
		default:
			return 0, errors.New("unfamiliar HEVC NAL type")
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if frames == 0 || !key {
		return 0, errors.New("HEVC contains no independent pictures")
	}
	return float64(frames) / 20, nil
}
func indexNALStart(data []byte, from int) (int, int) {
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
func splitIndexNAL(data []byte, atEOF bool) (int, []byte, error) {
	start, prefix := indexNALStart(data, 0)
	if start < 0 {
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("not Annex B HEVC")
		}
		return 0, nil, nil
	}
	if start != 0 {
		return 0, nil, errors.New("unexpected HEVC prefix bytes")
	}
	next, _ := indexNALStart(data, prefix)
	if next >= 0 {
		return next, data[:next], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
