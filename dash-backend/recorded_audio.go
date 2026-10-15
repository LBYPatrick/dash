package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A TS program can advertise AAC without ever recording a single audio sample.
// Only mux usable codec parameters; an empty declared track breaks FFmpeg's muxer.
func recordedAudio(ctx context.Context, file, cache string) (bool, error) {
	info, err := os.Stat(file)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d:audio-v1", file, info.Size(), info.ModTime().UnixNano())))
	target := filepath.Join(cache, fmt.Sprintf("%x.audio.json", hash))
	var found struct {
		Present bool `json:"present"`
	}
	if data, err := readSmallFile(target, 256); err == nil && json.Unmarshal(data, &found) == nil {
		return found.Present, nil
	}
	var output limitedBuffer
	args := []string{"-v", "error", "-threads", strconv.Itoa(ProbeThreads), "-select_streams", "a:0", "-show_entries", "stream=codec_name,sample_rate,channels", "-of", "json", file}
	if err := runMediaCommand(ctx, "ffprobe", &output, args...); err != nil {
		return false, err
	}
	var result struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Rate     string `json:"sample_rate"`
			Channels int    `json:"channels"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output.value, &result); err != nil {
		return false, err
	}
	for _, stream := range result.Streams {
		rate, _ := strconv.Atoi(stream.Rate)
		if stream.Codec == "aac" && rate > 0 && stream.Channels > 0 {
			found.Present = true
		}
	}
	data, _ := json.Marshal(found)
	if err := os.WriteFile(target, data, 0600); err != nil {
		return false, err
	}
	return found.Present, nil
}

// Use the same indexed camera durations for video and audio concatenation.
func (e *ExportManager) indexedVideoDuration(ctx context.Context, asset ExportAsset) (float64, error) {
	var raw string
	if e.index.db.QueryRowContext(ctx, "SELECT summary FROM segments WHERE name=?", filepath.Base(filepath.Dir(asset.Path))).Scan(&raw) == nil {
		var entry IndexedSegment
		if json.Unmarshal([]byte(raw), &entry) == nil {
			for _, file := range entry.Files {
				if file.Name == filepath.Base(asset.Path) && file.Duration > 0 {
					return file.Duration, nil
				}
			}
		}
	}
	return videoDuration(ctx, asset.Path)
}

// Start at the first usable AAC part and preserve missing-part intervals in
// the concat clock. An empty or absent microphone track must never shift later
// sound earlier, or make the video muxer fail while opening its first part.
func (e *ExportManager) audioManifest(ctx context.Context, camera string, assets []ExportAsset) (string, float64, error) {
	type part struct {
		path  string
		start float64
	}
	parts := []part{}
	offset := 0.0
	for _, asset := range assets {
		if asset.Size == 0 {
			continue
		}
		duration, err := e.indexedVideoDuration(ctx, asset)
		if err != nil {
			return "", 0, err
		}
		path := asset.Path
		if strings.HasSuffix(camera, ".hevc") {
			relative, _ := filepath.Rel(e.index.root, filepath.Join(filepath.Dir(asset.Path), "qcamera.ts"))
			path, err = safePath(e.index.root, relative)
		}
		present := false
		if err == nil {
			present, err = recordedAudio(ctx, path, e.folder)
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			if err != nil {
				log.Printf("audio unavailable for part %d; preserving video", asset.Segment)
			}
		}
		if present {
			parts = append(parts, part{path, offset})
		}
		offset += duration
	}
	if len(parts) == 0 {
		return "", 0, nil
	}
	var lines strings.Builder
	for n, part := range parts {
		end := offset
		if n+1 < len(parts) {
			end = parts[n+1].start
		}
		fmt.Fprintf(&lines, "file '%s'\nduration %.6f\n", strings.ReplaceAll(part.path, "'", "'\\''"), end-part.start)
	}
	return lines.String(), parts[0].start, nil
}
