package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Extract a small independently decodable GOP window instead of decoding the
// entire raw HEVC prefix for each preview. Index packet positions once, cached
// separately from the recording index. No full video is buffered in memory.
func hevcPreviewWindow(ctx context.Context, file, cache string, start float64) (path string, relative, audioStart float64, cleanup func(), err error) {
	cleanup = func() {}
	path, relative = file, start
	if start == 0 {
		return
	}
	info, err := os.Stat(file)
	if err != nil {
		return
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:seek-v1", file, info.Size(), info.ModTime().UnixNano())))
	indexPath := filepath.Join(cache, fmt.Sprintf("%x.seek.json", hash))
	var probe videoProbe
	raw, readErr := readSmallFile(indexPath, 4<<20)
	if readErr != nil || json.Unmarshal(raw, &probe) != nil || len(probe.Positions) != len(probe.Keys) || len(probe.Keys) == 0 {
		probe, err = probeMCAPVideo(ctx, file)
		if err != nil {
			return
		}
		raw, err = json.Marshal(probe)
		if err != nil {
			return
		}
		if err = os.WriteFile(indexPath, raw, 0600); err != nil {
			return
		}
	}
	if probe.BFrames {
		return
	} // Keep presentation-order safety for non-native footage.
	target := int(start * 20)
	if target >= len(probe.Positions) {
		err = errors.New("preview starts beyond recorded video")
		return
	}
	first := target
	for first > 0 && !probe.Keys[first] {
		first--
	}
	if first == 0 || !probe.Keys[first] || probe.Positions[first] <= 0 {
		return
	}
	source, err := os.Open(file)
	if err != nil {
		return
	}
	defer source.Close()
	// VPS/SPS/PPS are repeated at every independent window, even when the device
	// wrote them only once at the beginning of its raw stream.
	header := make([]byte, 64<<10)
	n, readErr := source.ReadAt(header, 0)
	if readErr != nil && readErr != io.EOF {
		err = readErr
		return
	}
	header = header[:n]
	parameters := []byte{}
	found := map[int]bool{}
	at, prefix := annexBStart(header, 0)
	for at >= 0 && at+prefix < len(header) {
		next, nextPrefix := annexBStart(header, at+prefix)
		if next < 0 {
			break
		}
		kind := int((header[at+prefix] >> 1) & 63)
		if kind <= 31 {
			break
		}
		if kind >= 32 && kind <= 34 {
			parameters = append(parameters, header[at:next]...)
			found[kind] = true
		}
		at, prefix = next, nextPrefix
	}
	if !found[32] || !found[33] || !found[34] {
		return
	}
	endFrame := min(target+PreviewChunkSeconds*20, len(probe.Positions))
	end := info.Size()
	if endFrame < len(probe.Positions) {
		end = probe.Positions[endFrame]
	}
	begin := probe.Positions[first]
	if begin < 0 || end <= begin || end > info.Size() {
		err = errors.New("invalid preview packet offsets")
		return
	}
	if err = requireRecordingSpace(cache, end-begin+int64(len(parameters))+PreviewChunkMaxBytes); err != nil {
		return
	}
	window, err := os.CreateTemp(cache, ".hevc-window-*.hevc")
	if err != nil {
		return
	}
	temporary := window.Name()
	cleanup = func() { _ = os.Remove(temporary) }
	output := &recordingSpaceWriter{path: cache, writer: window}
	if _, err = output.Write(parameters); err == nil {
		_, err = io.Copy(output, checkedReader{ctx, io.NewSectionReader(source, begin, end-begin)})
	}
	closeErr := window.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return
	}
	path = temporary
	audioStart = float64(first) / 20
	relative = start - audioStart
	return
}
