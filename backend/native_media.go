package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dash.local/backend/internal/playback"
	"dash.local/backend/internal/telemetry"
)

// NativeMedia is a small public descriptor, not the private packet inventory.
type NativeMedia struct {
	Key             string    `json:"key,omitempty"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	Codec           string    `json:"codec,omitempty"`
	Audio           bool      `json:"audio,omitempty"`
	AudioRate       uint32    `json:"audioRate,omitempty"`
	AudioChannels   byte      `json:"audioChannels,omitempty"`
	OriginMonotonic float64   `json:"originMonotonic,omitempty"`
	Fragments       []float64 `json:"fragments,omitempty"`
}

var nativeRequests = make(chan struct{}, NativeRequestLimit)
var nativeKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (i *Index) nativePath(key string) string { return filepath.Join(i.cache, "native", key+".index") }
func (i *Index) nativeComplete(entry IndexedSegment) bool {
	if entry.NativeVersion != playback.Version {
		return false
	}
	for _, file := range entry.Files {
		if !strings.HasSuffix(file.Name, ".hevc") || file.Size <= 0 || !cameras[file.Name] {
			continue
		}
		native := file.Native
		if native == nil {
			return false
		}
		if native.Status == "waitingForSpace" {
			return false
		}
		if native.Status != "ready" {
			continue
		}
		if !nativeKeyPattern.MatchString(native.Key) {
			return false
		}
		if _, repair := i.nativeRepair.Load(native.Key); repair {
			return false
		}
		info, err := os.Lstat(i.nativePath(native.Key))
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 32 || info.Size() > playback.MaxIndexBytes {
			return false
		}
	}
	return true
}
func (i *Index) prepareNative(ctx context.Context, folder, fingerprint string, entry *IndexedSegment) error {
	paths := []string{}
	needed := false
	for _, file := range entry.Files {
		if file.Size > 0 {
			paths = append(paths, filepath.Join(folder, file.Name))
		}
		if strings.HasSuffix(file.Name, ".hevc") && cameras[file.Name] && file.Size > 0 {
			needed = true
		}
	}
	entry.NativeVersion = playback.Version
	if !needed {
		return nil
	}
	if err := requireRecordingSpace(i.cache, 0); err != nil {
		for n := range entry.Files {
			file := &entry.Files[n]
			if strings.HasSuffix(file.Name, ".hevc") && cameras[file.Name] && file.Size > 0 {
				file.Native = &NativeMedia{Status: "waitingForSpace"}
			}
		}
		return nil
	}
	frames := map[string][]playback.FrameTime{}
	// One log pass supplies all cameras; logs are never opened by media requests.
	if logfile := telemetry.PreferredLog(paths, false); logfile != "" {
		err := telemetry.Stream(ctx, logfile, func(record telemetry.Record) error {
			if frame := record.Frame; frame != nil && frame.EndNS > 0 {
				if len(frames[frame.Camera]) >= playback.MaxSamples {
					return errors.New("native exposure inventory exceeds budget")
				}
				frames[frame.Camera] = append(frames[frame.Camera], playback.FrameTime{Index: frame.EncodeIndex, EndNS: frame.EndNS})
			}
			return nil
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !telemetry.IsIncomplete(err) {
			frames = map[string][]playback.FrameTime{}
		}
	}
	var audio playback.Audio
	var audioSize int64
	companion, err := safePath(i.root, filepath.Join(filepath.Base(folder), "qcamera.ts"))
	if err == nil {
		source, err := os.Open(companion)
		if err == nil {
			info, statErr := source.Stat()
			if statErr == nil && info.Mode().IsRegular() && info.Size() <= playback.MaxSourceBytes {
				audio, err = playback.InventoryAudio(ctx, source)
				audioSize = info.Size()
				if err != nil {
					log.Printf("native audio unavailable for %s: %v", filepath.Base(folder), err)
					audio = playback.Audio{}
				}
			}
			source.Close()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for number := range entry.Files {
		file := &entry.Files[number]
		if !strings.HasSuffix(file.Name, ".hevc") || !cameras[file.Name] || file.Size <= 0 {
			continue
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:native-v%d", filepath.Base(folder), fingerprint, file.Name, playback.Version)))
		key := fmt.Sprintf("%x", digest)
		file.Native = &NativeMedia{Key: key, Status: "unsupported"}
		if file.Size > playback.MaxSourceBytes {
			file.Native.Reason = "Native playback supports source parts up to 256 MiB. Download this original instead."
			continue
		}
		source, err := os.Open(filepath.Join(folder, file.Name))
		if err != nil {
			return err
		}
		video, err := playback.InventoryHEVC(ctx, source, frames[cameraTopics[file.Name]])
		source.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			file.Native.Reason = "This recording cannot be packaged for native HEVC playback. Use the road preview or download the original."
			log.Printf("native video unavailable for %s/%s: %v", filepath.Base(folder), file.Name, err)
			continue
		}
		last := video.Fragments[len(video.Fragments)-1]
		duration := last.Start + last.Duration
		shift := int64(0)
		if preview := frames["preview"]; len(preview) > 0 && video.OriginNS > 0 {
			first := preview[0].EndNS
			for _, frame := range preview {
				if frame.EndNS < first {
					first = frame.EndNS
				}
			}
			shift = int64(first) - int64(video.OriginNS)
		}
		index := &playback.Index{Version: playback.Version, Video: video, Audio: playback.CompleteAudio(audio, duration, shift), VideoSize: file.Size, AudioSize: audioSize}
		if ctx.Err() != nil || i.state() != "parked" {
			return context.Canceled
		}
		encoded, err := playback.EncodeIndex(index)
		if err != nil {
			file.Native.Reason = "Native playback exceeds the supported recording limits. Use the road preview or download the original."
			continue
		}
		if ctx.Err() != nil || i.state() != "parked" {
			return context.Canceled
		}
		if err := requireRecordingSpace(i.cache, int64(len(encoded))); err != nil {
			file.Native.Status = "waitingForSpace"
			continue
		}
		cacheFolder := filepath.Dir(i.nativePath(key))
		if err := os.MkdirAll(cacheFolder, 0700); err != nil {
			return err
		}
		if info, err := os.Lstat(cacheFolder); err != nil || !info.IsDir() {
			return errors.New("native cache must be a real app directory")
		}
		temporary, err := os.CreateTemp(cacheFolder, ".index-")
		if err != nil {
			return err
		}
		filename := temporary.Name()
		writeErr := func() error {
			defer temporary.Close()
			if _, err := temporary.Write(encoded); err != nil {
				return err
			}
			return temporary.Sync()
		}()
		if writeErr == nil && ctx.Err() == nil && i.state() == "parked" {
			writeErr = os.Rename(filename, i.nativePath(key))
		} else if writeErr == nil {
			writeErr = context.Canceled
		}
		os.Remove(filename)
		if writeErr != nil {
			return writeErr
		}
		// Flush the directory entry so a power cut cannot publish a database row
		// whose completed packet inventory was never durable.
		directory, err := os.Open(cacheFolder)
		if err != nil {
			return err
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return err
		}
		file.Native.Status = "ready"
		file.Native.Codec = video.Codec
		file.Native.Audio = len(index.Audio.Samples) > 0
		file.Native.AudioRate = index.Audio.Rate
		file.Native.AudioChannels = index.Audio.Channels
		file.Native.OriginMonotonic = float64(video.OriginNS) / 1e9
		for _, part := range video.Fragments {
			file.Native.Fragments = append(file.Native.Fragments, float64(part.Duration)/playback.Timescale)
		}
		file.Duration = float64(duration) / playback.Timescale
		i.nativeRepair.Delete(key)
		log.Printf("native indexed file=%s/%s codec=%s pictures=%d fragments=%d metadataBytes=%d audio=%t", filepath.Base(folder), file.Name, video.Codec, len(video.Samples), len(video.Fragments), len(encoded), file.Native.Audio)
	}
	return nil
}

func (s *Server) nativeMedia(w http.ResponseWriter, r *http.Request) {
	select {
	case nativeRequests <- struct{}{}:
		defer func() { <-nativeRequests }()
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 503, errors.New("too many simultaneous native media requests"))
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(NativeWriteTimeout))
	defer controller.SetWriteDeadline(time.Time{})
	relative := r.URL.Query().Get("file")
	name, camera := filepath.Base(filepath.Dir(relative)), filepath.Base(relative)
	if !segmentPattern.MatchString(name) || !cameras[camera] || !strings.HasSuffix(camera, ".hevc") || filepath.ToSlash(filepath.Join(name, camera)) != relative {
		fail(w, 400, errors.New("invalid native recording"))
		return
	}
	var raw string
	if err := s.index.db.QueryRowContext(r.Context(), "SELECT summary FROM segments WHERE name=?", name).Scan(&raw); err != nil {
		fail(w, 409, errors.New("native timeline is not indexed"))
		return
	}
	var entry IndexedSegment
	if json.Unmarshal([]byte(raw), &entry) != nil {
		fail(w, 409, errors.New("invalid cached timeline"))
		return
	}
	var file, companion FileInfo
	for _, candidate := range entry.Files {
		if candidate.Name == camera {
			file = candidate
		}
		if candidate.Name == "qcamera.ts" {
			companion = candidate
		}
	}
	native := file.Native
	if native == nil || native.Status != "ready" || !nativeKeyPattern.MatchString(native.Key) || native.Key != r.URL.Query().Get("key") {
		fail(w, 409, errors.New("native media needs parked preparation; reload the recording"))
		return
	}
	sourcePath, err := safePath(s.index.root, relative)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	video, err := os.Open(sourcePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer video.Close()
	info, err := video.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != file.Size || info.ModTime().UnixNano() != file.Mtime {
		fail(w, 409, errors.New("recording changed; reload its timeline"))
		return
	}
	data, err := readSmallFile(s.index.nativePath(native.Key), playback.MaxIndexBytes)
	var index *playback.Index
	if err == nil {
		index, err = playback.DecodeIndex(data)
	}
	if err != nil {
		s.index.nativeRepair.Store(native.Key, true)
		fail(w, 409, errors.New("cached packet inventory needs parked repair"))
		return
	}
	if value := r.URL.Query().Get("silence"); value != "" {
		parts := strings.Split(value, ":")
		if len(parts) != 2 || native.Audio {
			fail(w, 400, errors.New("invalid silent audio overlay"))
			return
		}
		rate, rateErr := strconv.ParseUint(parts[0], 10, 32)
		channels, channelErr := strconv.ParseUint(parts[1], 10, 8)
		if rateErr != nil || channelErr != nil || rate < 7350 || rate > 96000 || channels < 1 || channels > 2 {
			fail(w, 400, errors.New("invalid silent audio configuration"))
			return
		}
		index.Audio = playback.Audio{Object: 2, Rate: uint32(rate), Channels: byte(channels), Silence: true}
	}
	var audio *os.File
	if native.Audio {
		path, err := safePath(s.index.root, filepath.Join(name, "qcamera.ts"))
		if err == nil {
			audio, err = os.Open(path)
		}
		if err != nil {
			fail(w, 409, errors.New("recorded audio source changed"))
			return
		}
		defer audio.Close()
		stat, err := audio.Stat()
		if err != nil || !stat.Mode().IsRegular() || stat.Size() != companion.Size || stat.ModTime().UnixNano() != companion.Mtime {
			fail(w, 409, errors.New("recorded audio changed; reload its timeline"))
			return
		}
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", `"`+native.Key+"-"+r.URL.Query().Get("fragment")+"-"+r.URL.Query().Get("silence")+`"`)
	if r.URL.Path == "/api/media/init" {
		init, err := playback.Init(index)
		if err != nil {
			fail(w, 422, err)
			return
		}
		http.ServeContent(w, r, "init.mp4", time.Unix(0, file.Mtime), bytes.NewReader(init))
		return
	}
	number, err := strconv.Atoi(r.URL.Query().Get("fragment"))
	if err != nil {
		fail(w, 400, errors.New("invalid fragment number"))
		return
	}
	reader, err := playback.NewFragment(r.Context(), index, number, video, audio)
	if err != nil {
		fail(w, 400, err)
		return
	}
	http.ServeContent(w, r, "fragment.m4s", time.Unix(0, file.Mtime), reader)
}

// Native-only serving makes the removed H.264 preview endpoint explicit. Road
// TS footage is still served directly at its existing URL.
func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	file, err := safePath(s.index.root, r.URL.Query().Get("file"))
	if err != nil || !cameras[filepath.Base(file)] {
		http.NotFound(w, r)
		return
	}
	if !strings.HasSuffix(file, ".ts") {
		fail(w, 410, errors.New("converted previews were replaced by native HEVC; reload the recording playlist"))
		return
	}
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, file)
}

// A completed parked scan owns garbage collection. Partial files after a power
// cut and indexes for removed recordings never accumulate indefinitely.
func (i *Index) pruneNative(ctx context.Context, used map[string]bool) {
	folder := filepath.Join(i.cache, "native")
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() {
		return
	}
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if ctx.Err() != nil || i.state() != "parked" {
			return
		}
		if !entry.Type().IsRegular() {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".index")
		if (strings.HasSuffix(entry.Name(), ".index") && nativeKeyPattern.MatchString(key) && !used[key]) || strings.HasPrefix(entry.Name(), ".index-") {
			_ = os.Remove(filepath.Join(folder, entry.Name()))
		}
	}
}
