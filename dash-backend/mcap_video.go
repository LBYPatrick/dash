package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dash.local/backend/internal/telemetry"
	"github.com/foxglove/mcap/go/mcap"
)

const maxVideoFrame = 8 << 20
const maxVideoPackets = 12000 // At most ten minutes at the logger's 20 fps.

// JSON encoding is a Foxglove-supported representation of CompressedVideo.
// Go's []byte marshaler supplies the required base64 string.
const compressedVideoSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","title":"foxglove.CompressedVideo","type":"object","properties":{"timestamp":{"type":"object","title":"time","properties":{"sec":{"type":"integer","minimum":0},"nsec":{"type":"integer","minimum":0,"maximum":999999999}},"required":["sec","nsec"]},"frame_id":{"type":"string"},"data":{"type":"string","contentEncoding":"base64"},"format":{"type":"string","enum":["h264","h265"]}},"required":["timestamp","frame_id","data","format"]}`

type compressedVideo struct {
	Timestamp struct {
		Sec  uint64 `json:"sec"`
		Nsec uint64 `json:"nsec"`
	} `json:"timestamp"`
	FrameID string `json:"frame_id"`
	Data    []byte `json:"data"`
	Format  string `json:"format"`
}

var cameraTopics = map[string]string{
	"qcamera.ts": "preview", "fcamera.hevc": "road", "ecamera.hevc": "wide_road", "dcamera.hevc": "driver",
}

// Probe output is bounded, unlike buffering media or FFprobe's packet hex dumps.
type videoProbeBuffer struct{ value bytes.Buffer }

func (b *videoProbeBuffer) Bytes() []byte { return b.value.Bytes() }

func (b *videoProbeBuffer) Write(p []byte) (int, error) {
	if b.value.Len()+len(p) > 4<<20 {
		return 0, errors.New("video packet inventory exceeds 4 MiB")
	}
	return b.value.Write(p)
}

type videoProbe struct {
	Codec   string
	BFrames bool
	Offsets []float64
	Step    float64
}

func probeMCAPVideo(ctx context.Context, filename string) (videoProbe, error) {
	limited, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output videoProbeBuffer
	args := []string{"-max_alloc", "33554432", "-v", "error", "-threads", "1", "-select_streams", "v:0", "-show_packets", "-show_streams", "-show_entries", "packet=pts_time:stream=codec_name,has_b_frames,r_frame_rate", "-of", "json", filename}
	if err := runMediaCommand(limited, "ffprobe", &output, args...); err != nil {
		return videoProbe{}, err
	}
	var result struct {
		Streams []struct {
			Codec   string `json:"codec_name"`
			BFrames int    `json:"has_b_frames"`
			Rate    string `json:"r_frame_rate"`
		} `json:"streams"`
		Packets []struct {
			PTS string `json:"pts_time"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return videoProbe{}, err
	}
	if len(result.Streams) != 1 || len(result.Packets) == 0 || len(result.Packets) > maxVideoPackets {
		return videoProbe{}, errors.New("missing video or too many frames in a segment")
	}
	stream := result.Streams[0]
	if stream.Codec != "h264" && stream.Codec != "hevc" {
		return videoProbe{}, errors.New("MCAP camera must be H.264 or HEVC")
	}
	raw := strings.HasSuffix(filename, ".hevc")
	fps := 20.0
	if !raw {
		rate := strings.Split(stream.Rate, "/")
		if len(rate) != 2 {
			return videoProbe{}, errors.New("invalid camera frame rate")
		}
		n, _ := strconv.ParseFloat(rate[0], 64)
		d, _ := strconv.ParseFloat(rate[1], 64)
		fps = n / d
		if !finiteVideo(fps) || fps < 1 || fps > 120 {
			return videoProbe{}, errors.New("unsupported camera frame rate")
		}
	}
	probe := videoProbe{Codec: stream.Codec, BFrames: stream.BFrames > 0, Step: 1 / fps, Offsets: make([]float64, len(result.Packets))}
	for n, packet := range result.Packets {
		if raw {
			probe.Offsets[n] = float64(n) / 20
			continue
		}
		pts, err := strconv.ParseFloat(packet.PTS, 64)
		if err != nil || !finiteVideo(pts) {
			return videoProbe{}, errors.New("camera has no valid presentation timestamps")
		}
		probe.Offsets[n] = pts
	}
	// A transcoder outputs presentation order; a copied stream must already have it.
	if probe.BFrames {
		sort.Float64s(probe.Offsets)
	}
	first := probe.Offsets[0]
	for n := range probe.Offsets {
		probe.Offsets[n] -= first
		if probe.Offsets[n] < 0 || probe.Offsets[n] > 600 || (n > 0 && probe.Offsets[n] <= probe.Offsets[n-1]) {
			return videoProbe{}, errors.New("invalid or discontinuous video timestamps")
		}
	}
	return probe, nil
}
func finiteVideo(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// Camera exposure events are not decoded. These anchors align with Dash playback:
// first log event mapped to GPS UTC, otherwise a clearly documented mtime estimate.
func videoStart(asset ExportAsset, entry telemetry.Metadata, duration float64) uint64 {
	utc := float64(asset.Modified.UnixNano())/1e9 - duration
	if len(entry.Points) > 0 {
		anchor := entry.Points[0]
		utc = anchor.Time + entry.StartMonotonic - anchor.Monotonic
	}
	return uint64(max(0, utc*1e9))
}

type videoMessageWriter func(uint16, uint64, any) error

func (e *ExportManager) mcapVideos(ctx context.Context, writer *mcap.Writer, job *ExportJob, request ExportRequest, assets []ExportAsset, snapshot map[string]IndexedSegment, message videoMessageWriter) error {
	channels := map[string]uint16{}
	for _, asset := range assets {
		camera := filepath.Base(asset.Path)
		if _, ok := cameraTopics[camera]; !ok {
			continue
		}
		if len(channels) == 0 {
			if err := writer.WriteSchema(&mcap.Schema{ID: 4, Name: "foxglove.CompressedVideo", Encoding: "jsonschema", Data: []byte(compressedVideoSchema)}); err != nil {
				return err
			}
		}
		if channels[camera] != 0 {
			continue
		}
		id := uint16(4 + len(channels))
		channels[camera] = id
		if err := writer.WriteChannel(&mcap.Channel{ID: id, SchemaID: 4, Topic: "/dash/camera/" + cameraTopics[camera] + "/video", MessageEncoding: "json", Metadata: map[string]string{"source": camera, "timestamps": "GPS-aligned segment start + presentation offset; raw HEVC assumes 20 fps; without GPS uses mtime estimate"}}); err != nil {
			return err
		}
	}
	previousEnd := map[string]uint64{}
	for i, asset := range assets {
		camera := filepath.Base(asset.Path)
		channel := channels[camera]
		if channel == 0 {
			continue
		}
		e.update(job.ID, "running", fmt.Sprintf("Adding %s video · part %d", cameraTopics[camera], asset.Segment), float64(i)/float64(len(assets)))
		probe, err := probeMCAPVideo(ctx, asset.Path)
		if err != nil {
			return fmt.Errorf("%d/%s video: %w", asset.Segment, camera, err)
		}
		entry := snapshot[fmt.Sprintf("%s--%d", request.Drive, asset.Segment)].Metadata
		start := videoStart(asset, entry, probe.Offsets[len(probe.Offsets)-1]+probe.Step)
		estimatedStart := start
		// File mtimes (and occasionally log anchors) can overlap between parts.
		// Preserve frame order on each channel and disclose any adjustment.
		start = max(start, previousEnd[camera])
		previousEnd[camera] = start + uint64(math.Round((probe.Offsets[len(probe.Offsets)-1]+probe.Step)*1e9))
		basis := "file modification time minus video duration estimate"
		if len(entry.Points) > 0 {
			basis = "GPS UTC mapped to first log event; exposure timestamps unavailable"
		}
		if err := writer.WriteMetadata(&mcap.Metadata{Name: fmt.Sprintf("dash.video.%d.%s", asset.Segment, cameraTopics[camera]), Metadata: map[string]string{
			"timeBasis": basis, "estimatedStartNanoseconds": strconv.FormatUint(estimatedStart, 10), "startNanoseconds": strconv.FormatUint(start, 10),
			"overlapAdjusted": strconv.FormatBool(start != estimatedStart), "rawHEVC20fps": strconv.FormatBool(strings.HasSuffix(camera, ".hevc")), "bFramesConverted": strconv.FormatBool(probe.BFrames), "frames": strconv.Itoa(len(probe.Offsets)),
		}}); err != nil {
			return err
		}
		format := probe.Codec
		if format == "hevc" {
			format = "h265"
		}
		if probe.BFrames {
			format = "h264"
		}
		frame := 0
		err = streamMCAPVideo(ctx, asset.Path, probe, func(data []byte) error {
			if frame >= len(probe.Offsets) {
				return errors.New("more video frames than presentation timestamps")
			}
			timestamp := start + uint64(math.Round(probe.Offsets[frame]*1e9))
			value := compressedVideo{FrameID: "dash_" + cameraTopics[camera] + "_optical", Format: format, Data: data}
			value.Timestamp.Sec = timestamp / 1e9
			value.Timestamp.Nsec = timestamp % 1e9
			frame++
			return message(channel, timestamp, value)
		})
		if err != nil {
			return fmt.Errorf("%d/%s video: %w", asset.Segment, camera, err)
		}
		if frame != len(probe.Offsets) {
			return fmt.Errorf("%d/%s video: incomplete frames (%d of %d)", asset.Segment, camera, frame, len(probe.Offsets))
		}
	}
	return nil
}

func streamMCAPVideo(ctx context.Context, filename string, probe videoProbe, emit func([]byte) error) error {
	limited, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	format := probe.Codec
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-max_alloc", "33554432", "-threads", "1", "-filter_threads", "1"}
	if strings.HasSuffix(filename, ".hevc") {
		args = append(args, "-r", "20")
	}
	args = append(args, "-i", filename, "-map", "0:v:0", "-an", "-sn", "-dn")
	if probe.BFrames {
		format = "h264"
		args = append(args, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-crf", "23", "-pix_fmt", "yuv420p", "-bf", "0", "-fps_mode", "passthrough")
	} else {
		args = append(args, "-c:v", "copy")
	}
	filter := "h264_metadata=aud=insert"
	if format == "hevc" {
		filter = "hevc_metadata=aud=insert"
	}
	args = append(args, "-bsf:v", filter, "-f", format, "-flush_packets", "1", "pipe:1")
	reader, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runMediaCommand(limited, "ffmpeg", output, args...)
		_ = output.CloseWithError(err)
		done <- err
	}()
	parseErr := readAccessUnits(reader, format, emit)
	// Closing the read side and canceling before waiting guarantees no orphaned
	// worker even when a malformed frame or the output budget stops the consumer.
	_ = reader.Close()
	if parseErr != nil {
		cancel()
	}
	commandErr := <-done
	if parseErr != nil {
		return parseErr
	}
	return commandErr
}

func annexBStart(data []byte, from int) (int, int) {
	for n := from; n+2 < len(data); n++ {
		if data[n] == 0 && data[n+1] == 0 {
			if data[n+2] == 1 {
				return n, 3
			}
			if n+3 < len(data) && data[n+2] == 0 && data[n+3] == 1 {
				return n, 4
			}
		}
	}
	return -1, 0
}
func splitAnnexB(data []byte, atEOF bool) (int, []byte, error) {
	start, prefix := annexBStart(data, 0)
	if start < 0 {
		if atEOF && len(data) > 0 {
			return 0, nil, errors.New("video is not Annex B")
		}
		return 0, nil, nil
	}
	if start != 0 {
		return 0, nil, errors.New("unexpected bytes before video NAL")
	}
	next, _ := annexBStart(data, prefix)
	if next >= 0 {
		return next, data[:next], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
func readAccessUnits(reader io.Reader, codec string, emit func([]byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxVideoFrame)
	scanner.Split(splitAnnexB)
	frame := make([]byte, 0, 256<<10)
	parameters := map[int][]byte{}
	hasVCL, key := false, false
	present := map[int]bool{}
	flush := func() error {
		if !hasVCL {
			return nil
		}
		if key {
			required := []int{7, 8}
			if codec == "hevc" {
				required = []int{32, 33, 34}
			}
			prefix := []byte{}
			for _, kind := range required {
				if len(parameters[kind]) == 0 {
					return errors.New("video keyframe is missing decoder parameters")
				}
				if !present[kind] {
					prefix = append(prefix, parameters[kind]...)
				}
			}
			if len(prefix)+len(frame) > maxVideoFrame {
				return errors.New("video frame exceeds 8 MiB")
			}
			if len(prefix) > 0 {
				frame = append(prefix, frame...)
			}
		}
		if err := emit(frame); err != nil {
			return err
		}
		frame = frame[:0]
		hasVCL = false
		key = false
		clear(present)
		return nil
	}
	frames := 0
	for scanner.Scan() {
		nal := scanner.Bytes()
		_, prefix := annexBStart(nal, 0)
		if len(nal) <= prefix {
			return errors.New("empty video NAL")
		}
		kind := int(nal[prefix] & 31)
		aud := kind == 9
		vcl := kind >= 1 && kind <= 5
		parameter := kind == 7 || kind == 8
		isKey := kind == 5
		if codec == "hevc" {
			if len(nal) < prefix+2 {
				return errors.New("short HEVC NAL")
			}
			kind = int((nal[prefix] >> 1) & 63)
			aud = kind == 35
			vcl = kind <= 31
			parameter = kind >= 32 && kind <= 34
			isKey = kind >= 16 && kind <= 23
		}
		if aud && hasVCL {
			if err := flush(); err != nil {
				return err
			}
			frames++
		}
		if parameter {
			if len(nal) > 64<<10 {
				return errors.New("oversized decoder parameters")
			}
			parameters[kind] = append(parameters[kind][:0], nal...)
			present[kind] = true
		}
		if len(frame)+len(nal) > maxVideoFrame {
			return errors.New("video frame exceeds 8 MiB")
		}
		frame = append(frame, nal...)
		hasVCL = hasVCL || vcl
		key = key || isKey
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if hasVCL {
		frames++
		if err := flush(); err != nil {
			return err
		}
	}
	if frames == 0 {
		return errors.New("no video frames")
	}
	return nil
}
