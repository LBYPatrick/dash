package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func videoDuration(ctx context.Context, filename string) (float64, error) {
	limited, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if strings.HasSuffix(filename, ".hevc") {
		if duration, err := rawHEVCDuration(limited, filename); err == nil {
			return duration, nil
		}
		if limited.Err() != nil {
			return 0, limited.Err()
		}
	}
	// Containers carry a duration in their timestamps. Counting every packet
	// remains the fallback for unfamiliar raw HEVC, but wastes a full TS scan.
	if !strings.HasSuffix(filename, ".hevc") {
		var quick limitedBuffer
		err := runMediaCommand(limited, "ffprobe", &quick, "-v", "error", "-threads", strconv.Itoa(ProbeThreads), "-select_streams", "v:0", "-show_entries", "stream=index:format=duration", "-of", "json", filename)
		var result struct {
			Streams []struct {
				Index int `json:"index"`
			} `json:"streams"`
			Format struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}
		if err == nil && json.Unmarshal(quick.value, &result) == nil && len(result.Streams) == 1 {
			duration, _ := strconv.ParseFloat(result.Format.Duration, 64)
			if finiteVideo(duration) && duration > 0 && duration <= 600 {
				return duration, nil
			}
		}
		if limited.Err() != nil {
			return 0, limited.Err()
		}
	}
	var output limitedBuffer
	args := []string{"-max_alloc", strconv.Itoa(MediaAllocationLimitMiB << 20), "-v", "error", "-threads", strconv.Itoa(ProbeThreads), "-select_streams", "v:0", "-count_packets", "-show_entries", "stream=nb_read_packets,r_frame_rate:format=duration", "-of", "json", filename}
	if err := runMediaCommand(limited, "ffprobe", &output, args...); err != nil {
		return 0, err
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Packets string `json:"nb_read_packets"`
			Rate    string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output.value, &result); err != nil {
		return 0, err
	}
	duration, _ := strconv.ParseFloat(result.Format.Duration, 64)
	if strings.HasSuffix(filename, ".hevc") || duration <= 0 {
		if len(result.Streams) == 0 {
			return 0, errors.New("no video stream")
		}
		stream := result.Streams[0]
		packets, _ := strconv.ParseFloat(stream.Packets, 64)
		fps := float64(20) // Logger's raw HEVC streams are recorded at 20 fps.
		if !strings.HasSuffix(filename, ".hevc") {
			rate := strings.Split(stream.Rate, "/")
			if len(rate) == 2 {
				n, _ := strconv.ParseFloat(rate[0], 64)
				d, _ := strconv.ParseFloat(rate[1], 64)
				if d > 0 {
					fps = n / d
				}
			}
		}
		if fps > 0 {
			duration = packets / fps
		}
	}
	if duration <= 0 || duration > 600 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, errors.New("invalid or oversized video duration")
	}
	return duration, nil
}

// A VOD playlist presents all available camera files as one seekable recording.
// Discontinuities account for timestamps restarting at source file boundaries.
// The playlist reads cached durations only; it never starts a worker itself.
func (s *Server) playlist(w http.ResponseWriter, r *http.Request) {
	drive, camera := r.URL.Query().Get("drive"), r.URL.Query().Get("camera")
	if !segmentPattern.MatchString(drive+"--0") || !cameras[camera] {
		fail(w, 400, errors.New("invalid recording or camera"))
		return
	}
	snapshot, err := s.index.snapshot()
	if err != nil {
		fail(w, 500, err)
		return
	}
	inventory, err := os.ReadDir(s.index.root)
	if err != nil {
		fail(w, 500, err)
		return
	}
	available := map[string]bool{}
	for _, folder := range inventory {
		if folder.IsDir() && strings.HasPrefix(folder.Name(), drive+"--") && segmentPattern.MatchString(folder.Name()) {
			available[folder.Name()] = true
			entry, ok := snapshot.Segments[folder.Name()]
			if !ok || entry.MediaVersion != 1 {
				fail(w, 409, errors.New("recording timeline is still indexing; confirm parked state and wait"))
				return
			}
		}
	}
	type clip struct {
		index    int
		name     string
		duration float64
	}
	clips := []clip{}
	for name, entry := range snapshot.Segments {
		if !available[name] {
			continue
		}
		index, _ := strconv.Atoi(strings.TrimPrefix(name, drive+"--"))
		for _, file := range entry.Files {
			if file.Name != camera {
				continue
			}
			if file.Duration <= 0 {
				continue
			}
			clips = append(clips, clip{index, name, file.Duration})
		}
	}
	if len(clips) == 0 {
		fail(w, 404, errors.New("no indexed footage for this camera"))
		return
	}
	if len(clips) > 4096 {
		fail(w, 400, errors.New("recording exceeds playlist limit"))
		return
	}
	sort.Slice(clips, func(a, b int) bool { return clips[a].index < clips[b].index })
	native := strings.HasSuffix(camera, ".hevc")
	var sessionAudio *NativeMedia
	if native {
		for _, clip := range clips {
			for _, file := range snapshot.Segments[clip.name].Files {
				if file.Name == camera && file.Native != nil && file.Native.Audio {
					sessionAudio = file.Native
					break
				}
			}
		}
	}
	target := float64(1)
	for _, clip := range clips {
		if native {
			for _, file := range snapshot.Segments[clip.name].Files {
				if file.Name != camera {
					continue
				}
				if file.Native == nil || file.Native.Status != "ready" {
					fail(w, 409, errors.New("native HEVC timeline needs parked preparation or this recording is unsupported"))
					return
				}
				for _, duration := range file.Native.Fragments {
					target = max(target, math.Ceil(duration))
				}
			}
		} else {
			target = max(target, math.Ceil(clip.duration))
		}
	}
	var result strings.Builder
	version := 3
	if native {
		version = 7
	}
	fmt.Fprintf(&result, "#EXTM3U\n#EXT-X-VERSION:%d\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:%.0f\n#EXT-X-MEDIA-SEQUENCE:0\n", version, target)
	if native {
		result.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	}
	for number, clip := range clips {
		if number > 0 {
			result.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if !native {
			query := url.Values{"file": {filepath.ToSlash(filepath.Join(clip.name, camera))}, "container": {"ts"}}
			fmt.Fprintf(&result, "#EXTINF:%.6f,\n/api/video?%s\n", clip.duration, query.Encode())
			continue
		}
		var media *NativeMedia
		for _, file := range snapshot.Segments[clip.name].Files {
			if file.Name == camera {
				media = file.Native
			}
		}
		query := url.Values{"file": {filepath.ToSlash(filepath.Join(clip.name, camera))}, "key": {media.Key}}
		if sessionAudio != nil && !media.Audio {
			query.Set("silence", fmt.Sprintf("%d:%d", sessionAudio.AudioRate, sessionAudio.AudioChannels))
		}
		fmt.Fprintf(&result, "#EXT-X-MAP:URI=\"/api/media/init?%s\"\n", query.Encode())
		for fragment, duration := range media.Fragments {
			query.Set("fragment", strconv.Itoa(fragment))
			fmt.Fprintf(&result, "#EXTINF:%.6f,\n/api/media/fragment?%s\n", duration, query.Encode())
		}
	}
	result.WriteString("#EXT-X-ENDLIST\n")
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(result.String()))
	}
}
