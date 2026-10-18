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

	foxglove "dash.local/backend/internal/mcaptypes/foxglove"
	"dash.local/backend/internal/telemetry"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
)

const maxVideoFrame = 8 << 20
const maxVideoPackets = 12000 // At most ten minutes at the logger's 20 fps.

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
	Positions []int64
	Codec     string
	BFrames   bool
	Keys      []bool
	DropLast  bool
	Sizes     []int
	Offsets   []float64
	Step      float64
}

func probeMCAPVideo(ctx context.Context, filename string) (videoProbe, error) {
	limited, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output videoProbeBuffer
	args := []string{"-max_alloc", strconv.Itoa(MediaAllocationLimitMiB << 20), "-v", "error", "-threads", strconv.Itoa(ProbeThreads), "-select_streams", "v:0", "-show_packets", "-show_streams", "-show_entries", "packet=pts_time,size,flags,pos:stream=codec_name,has_b_frames,r_frame_rate", "-of", "json", filename}
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
			Pos   string `json:"pos"`
			PTS   string `json:"pts_time"`
			Size  string `json:"size"`
			Flags string `json:"flags"`
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
	probe := videoProbe{Positions: make([]int64, len(result.Packets)), Codec: stream.Codec, BFrames: stream.BFrames > 0, Step: 1 / fps, Offsets: make([]float64, len(result.Packets)), Sizes: make([]int, len(result.Packets)), Keys: make([]bool, len(result.Packets))}
	for n, packet := range result.Packets {
		probe.Positions[n], _ = strconv.ParseInt(packet.Pos, 10, 64)
		probe.Sizes[n], _ = strconv.Atoi(packet.Size)
		probe.Keys[n] = strings.Contains(packet.Flags, "K")
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

// A damaged packet can disappear from decoder output in the middle of a
// stream. Match frame packet positions instead of sliding subsequent timestamps
// over the gap. Complete logs retain strict original frame-count validation.
func probeDecodedMCAPFrames(ctx context.Context, filename string, probe videoProbe) ([]int, error) {
	limited, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var output videoProbeBuffer
	args := []string{"-v", "error", "-threads", strconv.Itoa(ProbeThreads), "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=pkt_pos", "-of", "json", filename}
	if err := runMediaCommand(limited, "ffprobe", &output, args...); err != nil {
		return nil, err
	}
	var result struct {
		Frames []struct {
			Position string `json:"pkt_pos"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return nil, err
	}
	positions := make([]int64, 0, len(result.Frames))
	for _, frame := range result.Frames {
		position, err := strconv.ParseInt(frame.Position, 10, 64)
		if err != nil {
			return nil, errors.New("decoded frame has no source position")
		}
		positions = append(positions, position)
	}
	return decodedMCAPFrameIndexes(probe, positions)
}

func decodedMCAPFrameIndexes(probe videoProbe, positions []int64) ([]int, error) {
	if probe.BFrames {
		return nil, errors.New("cannot recover damaged B-frame stream timing")
	}
	available := make(map[int64]int, len(probe.Positions))
	for n, position := range probe.Positions {
		available[position] = n
	}
	kept := []int{}
	last := -1
	for _, position := range positions {
		original, ok := available[position]
		if !ok || original <= last {
			return nil, errors.New("decoded frame positions do not match packet order")
		}
		last = original
		if original < len(probe.Offsets) {
			kept = append(kept, original)
		}
	}
	if len(kept) == 0 {
		return nil, errors.New("recording contains no decodable frames")
	}
	return kept, nil
}

func finiteVideo(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

type videoMessageWriter func(uint16, uint64, proto.Message) error

func (e *ExportManager) mcapVideos(ctx context.Context, writer *mcap.Writer, job *ExportJob, assets []ExportAsset, clocks map[int]mcapClock, logs map[int]string, message videoMessageWriter) error {
	channels := map[string]uint16{}
	for _, asset := range assets {
		camera := filepath.Base(asset.Path)
		if _, ok := cameraTopics[camera]; !ok || camera == "qcamera.ts" || asset.Size == 0 {
			continue
		}
		if len(channels) == 0 {
			if err := writeProtobufSchema(writer, videoSchemaID, &foxglove.CompressedVideo{}); err != nil {
				return err
			}
		}
		if channels[camera] != 0 {
			continue
		}
		id := videoSchemaID + uint16(len(channels))
		channels[camera] = id
		if err := writer.WriteChannel(&mcap.Channel{ID: id, SchemaID: videoSchemaID, Topic: "/dash/camera/" + cameraTopics[camera] + "/video", MessageEncoding: "protobuf", Metadata: map[string]string{"source": camera, "timestamps": "Recorded EncodeIdx end-of-frame clock when complete; otherwise disclosed segment-start + presentation-offset estimate"}}); err != nil {
			return err
		}
	}
	previousEnd := map[string]uint64{}
	previousStep := map[string]uint64{}
	cachedSegment := -1
	var frameIndexes map[string][]telemetry.FrameIndex
	var partialIndexes bool
	for i, asset := range assets {
		camera := filepath.Base(asset.Path)
		channel := channels[camera]
		if channel == 0 || asset.Size == 0 {
			continue
		}
		e.update(job.ID, "running", fmt.Sprintf("Adding %s video · part %d", cameraTopics[camera], asset.Segment), float64(i)/float64(len(assets)))
		probe, err := probeMCAPVideo(ctx, asset.Path)
		if err != nil {
			return fmt.Errorf("%d/%s video: %w", asset.Segment, camera, err)
		}
		clock := clocks[asset.Segment]
		if cachedSegment != asset.Segment {
			frameIndexes, err = loadMCAPFrameIndexes(ctx, logs[asset.Segment])
			partialIndexes = telemetry.IsIncomplete(err)
			if err != nil && !partialIndexes {
				return err
			}
			cachedSegment = asset.Segment
		}
		indexes := frameIndexes[cameraTopics[camera]]
		timings, err := recordedFrameTimes(indexes, len(probe.Offsets), clock)
		recoveredTiming := false
		if partialIndexes && len(indexes) > 1 && !probe.BFrames {
			timings, err = recoveredFrameTimes(indexes, probe, clock)
			recoveredTiming = err == nil && len(timings) > 0
			if recoveredTiming && len(timings) < len(probe.Offsets) {
				probe.DropLast = true
				probe.Offsets = probe.Offsets[:len(timings)]
			}
		}
		if err != nil {
			return fmt.Errorf("%d/%s: %w", asset.Segment, camera, err)
		}
		start := clock.timestamp(clock.Monotonic)
		estimatedStart := start
		basis := clock.Basis + "; estimated presentation offsets"
		if len(timings) > 0 {
			start = timings[0]
			basis = clock.Basis + "; recorded EncodeIdx timestampEof"
			if recoveredTiming {
				basis += "; interrupted log/video: verified index prefix, unlogged tail estimated from measured frame rate"
			}
			if start < previousEnd[camera] {
				return fmt.Errorf("%d/%s: recorded frames overlap previous part", asset.Segment, camera)
			}
		} else {
			if previousEnd[camera] > 0 {
				start = max(start, previousEnd[camera]+previousStep[camera]-1)
			}
			for _, offset := range probe.Offsets {
				timings = append(timings, start+uint64(math.Round(offset*1e9)))
			}
		}
		undecodable := 0
		if partialIndexes {
			decoded, decodeErr := probeDecodedMCAPFrames(ctx, asset.Path, probe)
			if decodeErr != nil {
				return fmt.Errorf("%d/%s decoded frames: %w", asset.Segment, camera, decodeErr)
			}
			undecodable = len(probe.Offsets) - len(decoded)
			keptTimes := make([]uint64, 0, len(decoded))
			keptOffsets := make([]float64, 0, len(decoded))
			for _, original := range decoded {
				keptTimes = append(keptTimes, timings[original])
				keptOffsets = append(keptOffsets, probe.Offsets[original])
			}
			timings, probe.Offsets = keptTimes, keptOffsets
			start = timings[0]
			if undecodable > 0 {
				basis += "; undecodable packets omitted, surviving capture times retained"
			}
		}
		previousEnd[camera] = timings[len(timings)-1] + 1
		previousStep[camera] = uint64(math.Round(probe.Step * 1e9))
		if err := writer.WriteMetadata(&mcap.Metadata{Name: fmt.Sprintf("dash.video.%d.%s", asset.Segment, cameraTopics[camera]), Metadata: map[string]string{
			"timeBasis": basis, "estimatedStartNanoseconds": strconv.FormatUint(estimatedStart, 10), "startNanoseconds": strconv.FormatUint(start, 10),
			"droppedIncompletePackets": strconv.FormatBool(probe.DropLast), "partialLog": strconv.FormatBool(partialIndexes), "recoveredTiming": strconv.FormatBool(recoveredTiming), "recordedIndexes": strconv.Itoa(len(indexes)),
			"overlapAdjusted": strconv.FormatBool(len(indexes) == 0 && start != estimatedStart), "rawHEVC20fps": strconv.FormatBool(len(indexes) == 0 && strings.HasSuffix(camera, ".hevc")), "bFramesConverted": strconv.FormatBool(probe.BFrames), "frames": strconv.Itoa(len(probe.Offsets)), "undecodablePackets": strconv.Itoa(undecodable), "outputCodec": "h264", "keyframeIntervalFrames": strconv.Itoa(MCAPKeyframeIntervalFrames),
		}}); err != nil {
			return err
		}
		frame := 0
		err = streamMCAPVideo(ctx, asset.Path, probe, func(data []byte) error {
			if frame >= len(probe.Offsets) {
				return errors.New("more video frames than presentation timestamps")
			}
			timestamp := timings[frame]
			value := &foxglove.CompressedVideo{Timestamp: protobufTime(timestamp), FrameId: "dash_" + cameraTopics[camera] + "_optical", Format: "h264", Data: data}
			frame++
			return message(channel, timestamp, value)
		})
		if err != nil {
			return fmt.Errorf("%d/%s video: %w", asset.Segment, camera, err)
		}
		expectedFrames := len(probe.Offsets)
		if frame != expectedFrames {
			return fmt.Errorf("%d/%s video: incomplete frames (%d of %d)", asset.Segment, camera, frame, len(probe.Offsets))
		}
	}
	return nil
}

func streamMCAPVideo(ctx context.Context, filename string, probe videoProbe, emit func([]byte) error) error {
	limited, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-max_alloc", strconv.Itoa(MediaAllocationLimitMiB << 20), "-threads", strconv.Itoa(MediaThreads), "-filter_threads", strconv.Itoa(MediaThreads)}
	if strings.HasSuffix(filename, ".hevc") {
		args = append(args, "-r", "20")
	}
	args = append(args, "-i", filename, "-map", "0:v:0", "-an", "-sn", "-dn")
	// Foxglove may resume a paused seek at any individual message, without the
	// preceding GOP. Every message therefore needs its own IDR and parameters.
	// Normalize HEVC as well to avoid platform-dependent HEVC decoder support.
	args = append(args, "-c:v", "libx264", "-threads", strconv.Itoa(MediaThreads),
		"-preset", "ultrafast", "-tune", "zerolatency", "-crf", "23", "-pix_fmt", "yuv420p",
		"-bf", "0", "-g", strconv.Itoa(MCAPKeyframeIntervalFrames), "-keyint_min", "1",
		"-sc_threshold", "0", "-x264-params", "repeat-headers=1:open-gop=0",
		"-fps_mode", "passthrough", "-frames:v", strconv.Itoa(len(probe.Offsets)),
		"-bsf:v", "h264_metadata=aud=insert", "-f", "h264", "-flush_packets", "1", "pipe:1")
	reader, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runMediaCommand(limited, "ffmpeg", output, args...)
		_ = output.CloseWithError(err)
		done <- err
	}()
	parseErr := readAccessUnits(reader, "h264", emit)
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

// Only a single part's frame indexes are retained. Encoder segment numbers are
// not directory numbers: loggerd subtracts a per-encoder offset upstream.
func loadMCAPFrameIndexes(ctx context.Context, path string) (map[string][]telemetry.FrameIndex, error) {
	frames := map[string][]telemetry.FrameIndex{}
	if path == "" {
		return frames, nil
	}
	err := telemetry.Stream(ctx, path, func(record telemetry.Record) error {
		if record.Frame == nil {
			return nil
		}
		frame := record.Frame
		if len(frames[frame.Camera]) >= maxVideoPackets {
			return errors.New("camera index exceeds frame budget")
		}
		frames[frame.Camera] = append(frames[frame.Camera], *frame)
		return nil
	})
	return frames, err
}
func recordedFrameTimes(frames []telemetry.FrameIndex, count int, clock mcapClock) ([]uint64, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	if len(frames) != count {
		return nil, fmt.Errorf("recorded camera index has %d frames but video has %d", len(frames), count)
	}
	sort.Slice(frames, func(a, b int) bool { return frames[a].PresentationIndex < frames[b].PresentationIndex })
	times := make([]uint64, len(frames))
	for n, frame := range frames {
		if frame.EndNS == 0 || frame.EndNS > math.MaxInt64 || frame.StartNS > frame.EndNS || (n > 0 && (frame.PresentationIndex <= frames[n-1].PresentationIndex || frame.EndNS <= frames[n-1].EndNS || frame.EncoderSegment != frames[0].EncoderSegment)) {
			return nil, errors.New("invalid or duplicate camera exposure index")
		}
		times[n] = clock.timestampNS(frame.EndNS)
	}
	return times, nil
}

// Interrupted logger files and camera buffers can stop at different points.
// Match a verified prefix by EncodeIdx order and payload sizes before using
// exposure times. AVC transport packets add AUD bytes; HEVC may prepend headers
// to the first keyframe. Never use this recovery path for a complete log.
func recoveredFrameTimes(frames []telemetry.FrameIndex, probe videoProbe, clock mcapClock) ([]uint64, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	sorted := append([]telemetry.FrameIndex(nil), frames...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PresentationIndex < sorted[j].PresentationIndex })
	count := min(len(sorted), len(probe.Offsets))
	if count < 2 || probe.BFrames {
		return nil, errors.New("cannot verify interrupted camera index alignment")
	}
	dropLast := false
	keyOverhead := 0
	if len(probe.Sizes) > 0 {
		keyOverhead = probe.Sizes[0] - int(sorted[0].Bytes)
	}
	for n := 0; n < count; n++ {
		frame := sorted[n]
		if n > 0 && frame.Bytes > 0 && (n < len(probe.Sizes)) {
			difference := probe.Sizes[n] - int(frame.Bytes)
			// H.264 MPEG-TS introduces a six-byte access-unit delimiter.
			knownOverhead := probe.Codec == "h264" && (difference == 6 || (n < len(probe.Keys) && probe.Keys[n] && difference == keyOverhead && keyOverhead >= 6 && keyOverhead <= 4096))
			if difference != 0 && !knownOverhead {
				// The final packet may be the interrupted write itself. It is
				// omitted from video messages, but retained in the raw attachment.
				if n != len(probe.Offsets)-1 {
					return nil, errors.New("interrupted camera payload differs from recorded index")
				}
				count = n
				dropLast = true
				break
			}
		}
	}
	times, err := recordedFrameTimes(sorted[:count], count, clock)
	if err != nil {
		return nil, err
	}
	target := len(probe.Offsets)
	if dropLast {
		target--
	}
	for len(times) < target {
		times = append(times, times[len(times)-1]+uint64(math.Round(probe.Step*1e9)))
	}
	return times, nil
}
