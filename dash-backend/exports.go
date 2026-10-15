package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	dashv2 "dash.local/backend/internal/mcaptypes/dashv2"
	"github.com/foxglove/mcap/go/mcap"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

const maxExportBytes int64 = 8 << 30

type ExportRequest struct {
	Drive    string `json:"drive"`
	Segments []int  `json:"segments"`
	Format   string `json:"format"`
	Camera   string `json:"camera"`
	H264     bool   `json:"h264"`
}
type ExportAsset struct {
	Segment  int
	Path     string
	Size     int64
	Modified time.Time
}
type ExportJob struct {
	ID       string  `json:"id"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
	Format   string  `json:"format"`
	Name     string  `json:"name"`
	Created  int64   `json:"created"`
	Bytes    int64   `json:"bytes"`
	output   string
	cancel   context.CancelFunc
}
type ExportManager struct {
	index       *Index
	folder      string
	mu          sync.Mutex
	jobs        map[string]*ExportJob
	rootContext context.Context
}

func newExports(ctx context.Context, index *Index) (*ExportManager, error) {
	folder := filepath.Join(index.cache, "exports")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return nil, err
	}
	return &ExportManager{index: index, folder: folder, jobs: map[string]*ExportJob{}, rootContext: ctx}, nil
}
func (e *ExportManager) busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, job := range e.jobs {
		if job.Status == "queued" || job.Status == "running" {
			return true
		}
	}
	return false
}
func (e *ExportManager) snapshot(id string) (ExportJob, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[id]
	if !ok {
		return ExportJob{}, os.ErrNotExist
	}
	return *job, nil
}
func (e *ExportManager) update(id, status, message string, progress float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job := e.jobs[id]
	if job.Status != status {
		log.Printf("export %s format=%s status=%s: %s", id, job.Format, status, strings.ReplaceAll(message, "\n", " "))
	}
	job.Status = status
	job.Message = message
	job.Progress = progress
}
func (e *ExportManager) cancelJob(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[id]
	if !ok {
		return os.ErrNotExist
	}
	job.cancel()
	return nil
}
func (e *ExportManager) collect(request ExportRequest) ([]ExportAsset, error) {
	if request.Format != "zip" && request.Format != "mp4" && request.Format != "mcap" && request.Format != "raw" {
		return nil, errors.New("unsupported export format")
	}
	if !segmentPattern.MatchString(request.Drive+"--0") || len(request.Segments) == 0 || len(request.Segments) > 10000 {
		return nil, errors.New("invalid drive or segment range")
	}
	if (request.Format == "mp4" || request.Format == "raw") && !cameras[request.Camera] {
		return nil, errors.New("unknown camera")
	}
	segments := append([]int{}, request.Segments...)
	sort.Ints(segments)
	assets := []ExportAsset{}
	seen := map[int]bool{}
	for _, number := range segments {
		if number < 0 || seen[number] {
			return nil, errors.New("invalid or duplicate segment")
		}
		seen[number] = true
		name := fmt.Sprintf("%s--%d", request.Drive, number)
		folder, err := safePath(e.index.root, name)
		if err != nil {
			return nil, errors.New("selected segment is unavailable")
		}
		files, err := os.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		found := false
		for _, file := range files {
			if file.IsDir() || ((request.Format == "mp4" || request.Format == "raw") && file.Name() != request.Camera) {
				continue
			}
			path, err := safePath(e.index.root, filepath.Join(name, file.Name()))
			if err != nil {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			assets = append(assets, ExportAsset{number, path, info.Size(), info.ModTime()})
			found = true
		}
		if !found {
			return nil, errors.New("selected segment has no matching recordings")
		}
	}
	var size int64
	for _, asset := range assets {
		size += asset.Size
	}
	if size > maxExportBytes {
		return nil, errors.New("complete session exceeds the 8 GiB export limit")
	}
	var disk unix.Statfs_t
	if err := unix.Statfs(e.folder, &disk); err == nil && uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(size*2+(1<<30)) {
		return nil, errors.New("insufficient temporary space; 1 GiB is reserved for the device")
	}
	return assets, nil
}
func (e *ExportManager) submit(request ExportRequest) (ExportJob, error) {
	if e.index.state() != "parked" {
		return ExportJob{}, errors.New("exports require confirmed parked state")
	}
	// Resolve the full session from disk, never from a stale browser inventory
	// or a caller-provided range. Keep numeric ordering for video and telemetry.
	if !segmentPattern.MatchString(request.Drive + "--0") {
		return ExportJob{}, errors.New("invalid session")
	}
	inventory, err := os.ReadDir(e.index.root)
	if err != nil {
		return ExportJob{}, err
	}
	request.Segments = nil
	for _, folder := range inventory {
		match := segmentPattern.FindStringSubmatch(folder.Name())
		if !folder.IsDir() || len(match) != 3 || match[1] != request.Drive {
			continue
		}
		number, err := strconv.Atoi(match[2])
		if err != nil {
			return ExportJob{}, errors.New("invalid session part")
		}
		request.Segments = append(request.Segments, number)
	}
	sort.Ints(request.Segments)
	assets, err := e.collect(request)
	if err != nil {
		return ExportJob{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, job := range e.jobs {
		if job.Status == "queued" || job.Status == "running" {
			return ExportJob{}, errors.New("another export is running; finish or cancel it first")
		}
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ExportJob{}, err
	}
	id := hex.EncodeToString(bytes)
	ctx, cancel := parkedContext(e.rootContext, e.index.state)
	extension := request.Format
	name := request.Drive + "." + extension
	if request.Format == "raw" {
		extension = "zip"
		name = request.Drive + "-raw-video.zip"
	}
	job := &ExportJob{ID: id, Status: "queued", Message: "Queued", Format: request.Format, Name: name, Created: time.Now().Unix(), output: filepath.Join(e.folder, id+"."+extension), cancel: cancel}
	e.jobs[id] = job
	go e.run(ctx, job, request, assets)
	return *job, nil
}
func (e *ExportManager) run(ctx context.Context, job *ExportJob, request ExportRequest, assets []ExportAsset) {
	defer job.cancel()
	e.update(job.ID, "running", "Preparing export", 0)
	release, err := acquireCompute(ctx)
	if err != nil {
		e.update(job.ID, "failed", err.Error(), 0)
		return
	}
	defer release()
	switch request.Format {
	case "zip", "raw":
		err = e.zip(ctx, job, request, assets)
	case "mp4":
		err = e.mp4(ctx, job, request, assets)
	case "mcap":
		err = e.mcap(ctx, job, request, assets)
	}
	if ctx.Err() != nil {
		err = errors.New("export stopped: canceled or vehicle no longer confirmed parked")
	}
	if err != nil {
		_ = os.Remove(job.output)
		status := "failed"
		if ctx.Err() != nil {
			status = "canceled"
		}
		e.update(job.ID, status, err.Error(), 0)
		return
	}
	info, err := os.Stat(job.output)
	if err != nil {
		e.update(job.ID, "failed", err.Error(), 0)
		return
	}
	e.mu.Lock()
	job.Bytes = info.Size()
	e.mu.Unlock()
	e.update(job.ID, "complete", "Ready to download", 1)
}
func (e *ExportManager) cleanup() {
	if e.index.state() != "parked" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	entries, _ := os.ReadDir(e.folder)
	for _, file := range entries {
		if e.index.state() != "parked" {
			return
		}
		info, err := file.Info()
		if err == nil && !info.IsDir() && time.Since(info.ModTime()) > time.Hour {
			active := false
			for _, job := range e.jobs {
				if job.Status == "running" || job.Status == "queued" {
					active = true
					break
				}
			}
			if !active {
				_ = os.Remove(filepath.Join(e.folder, file.Name()))
			}
		}
	}
	for id, job := range e.jobs {
		if e.index.state() != "parked" {
			return
		}
		if job.Status != "running" && job.Status != "queued" && time.Now().Unix()-job.Created > 3600 {
			_ = os.Remove(job.output)
			delete(e.jobs, id)
		}
	}
}

type checkedReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r checkedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type boundedWriter struct {
	ctx    context.Context
	writer io.Writer
	size   int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if w.size+int64(len(p)) > maxExportBytes {
		return 0, errors.New("output exceeds 8 GiB")
	}
	n, err := w.writer.Write(p)
	w.size += int64(n)
	return n, err
}
func copyAsset(ctx context.Context, asset ExportAsset, target io.Writer) error {
	file, err := os.Open(asset.Path)
	if err != nil {
		return err
	}
	defer file.Close()
	n, err := io.CopyBuffer(target, checkedReader{ctx, io.LimitReader(file, asset.Size)}, make([]byte, 256<<10))
	if err == nil && n != asset.Size {
		return errors.New("recording changed during export")
	}
	return err
}
func (e *ExportManager) zip(ctx context.Context, job *ExportJob, request ExportRequest, assets []ExportAsset) error {
	file, err := os.Create(job.output)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := zip.NewWriter(&boundedWriter{ctx: ctx, writer: file})
	manifest, err := archive.Create("dash-manifest.json")
	if err != nil {
		return err
	}
	if err = json.NewEncoder(manifest).Encode(request); err != nil {
		return err
	}
	for i, asset := range assets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := fmt.Sprintf("%d/%s", asset.Segment, filepath.Base(asset.Path))
		e.update(job.ID, "running", "Archiving "+name, float64(i)/float64(len(assets)))
		entry, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: asset.Modified})
		if err != nil {
			return err
		}
		if err := copyAsset(ctx, asset, entry); err != nil {
			return err
		}
	}
	return archive.Close()
}
func (e *ExportManager) mp4(ctx context.Context, job *ExportJob, request ExportRequest, assets []ExportAsset) error {
	listing := filepath.Join(e.folder, job.ID+".txt")
	defer os.Remove(listing)
	var lines strings.Builder
	for _, asset := range assets {
		if asset.Size == 0 {
			continue
		}
		duration, err := e.indexedVideoDuration(ctx, asset)
		if err != nil {
			return err
		}
		fmt.Fprintf(&lines, "file '%s'\noption probesize %d\noption analyzeduration %d\nduration %.6f\n", strings.ReplaceAll(asset.Path, "'", "'\\''"), AudioProbeBytes, AudioAnalyzeMicros, duration)
	}
	if lines.Len() == 0 {
		return errors.New("session has no non-empty video recordings")
	}
	if err := os.WriteFile(listing, []byte(lines.String()), 0600); err != nil {
		return err
	}
	args := append(audioProbeArgs(), "-fflags", "+genpts", "-f", "concat", "-safe", "0", "-i", listing)
	if strings.HasSuffix(request.Camera, ".hevc") {
		args = append([]string{"-r", "20"}, args...)
	}
	audioLines, audioOffset, err := e.audioManifest(ctx, request.Camera, assets)
	if err != nil {
		return err
	}
	if audioLines != "" {
		audioListing := listing + ".audio.txt"
		defer os.Remove(audioListing)
		if err := os.WriteFile(audioListing, []byte(audioLines), 0600); err != nil {
			return err
		}
		args = append(args, audioProbeArgs()...)
		args = append(args, "-itsoffset", strconv.FormatFloat(audioOffset, 'f', 6, 64), "-f", "concat", "-safe", "0", "-i", audioListing, "-map", "1:a:0", "-c:a", "copy")
	} else {
		args = append(args, "-an")
	}

	args = append(args, "-map", "0:v:0")
	if request.H264 {
		args = append(args, "-c:v", "libx264", "-threads", strconv.Itoa(MediaThreads), "-preset", "ultrafast", "-crf", "23", "-pix_fmt", "yuv420p")
	} else {
		args = append(args, "-c:v", "copy")
		if strings.HasSuffix(request.Camera, ".hevc") {
			args = append(args, "-tag:v", "hvc1")
		}
	}
	args = append(args, "-movflags", "+faststart", job.output)
	e.update(job.ID, "running", "Packaging selected camera as MP4", 0)
	limited, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-limited.Done():
				return
			case <-ticker.C:
				if info, err := os.Stat(job.output); err == nil && info.Size() > maxExportBytes {
					cancel()
					return
				}
			}
		}
	}()
	return runFFmpeg(limited, args...)
}

func (e *ExportManager) mcap(ctx context.Context, job *ExportJob, request ExportRequest, assets []ExportAsset) error {
	file, err := os.Create(job.output)
	if err != nil {
		return err
	}
	defer file.Close()
	writer, err := mcap.NewWriter(&boundedWriter{ctx: ctx, writer: file}, &mcap.WriterOptions{Chunked: true, ChunkSize: 1 << 20, IncludeCRC: true})
	if err != nil {
		return err
	}
	if err := writer.WriteHeader(&mcap.Header{Profile: "dash.v2", Library: "dash-backend/0.1"}); err != nil {
		return err
	}
	if err := mcapTelemetrySchemas(writer); err != nil {
		return err
	}
	if err := writer.WriteMetadata(&mcap.Metadata{Name: "dash.drive", Metadata: map[string]string{"id": request.Drive, "recordings": "Typed Protobuf messages at recorded rates, with original video/log attachments. Video uses EncodeIdx EOF timing when complete; otherwise disclosed presentation-offset estimates.", "timeBasis": "GPS UTC mapped to log monotonic time; without GPS, one shared session file-time estimate. Asset LogTime is capture start; filesystem dates appear only in attachment CreateTime and Asset.modified_time_nanoseconds."}}); err != nil {
		return err
	}
	sequence := uint32(0)
	message := func(channel uint16, timestamp uint64, value proto.Message) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := proto.Marshal(value)
		if err != nil {
			return err
		}
		sequence++
		return writer.WriteMessage(&mcap.Message{ChannelID: channel, Sequence: sequence, LogTime: timestamp, PublishTime: timestamp, Data: data})
	}
	snapshot, err := e.index.driveMetadata(request.Drive)
	if err != nil {
		return err
	}
	logs, clocks, err := resolveMCAPLogs(ctx, request, assets, snapshot)
	if err != nil {
		return err
	}
	for i, asset := range assets {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := fmt.Sprintf("%d/%s", asset.Segment, filepath.Base(asset.Path))
		e.update(job.ID, "running", "Packing "+name, float64(i)/float64(len(assets)))
		source, err := os.Open(asset.Path)
		if err != nil {
			return err
		}
		clock := clocks[asset.Segment]
		timestamp := clock.timestamp(clock.Monotonic)
		modified := uint64(max(int64(0), asset.Modified.UnixNano()))
		err = writer.WriteAttachment(&mcap.Attachment{Name: name, LogTime: timestamp, CreateTime: modified, MediaType: "application/octet-stream", DataSize: uint64(asset.Size), Data: checkedReader{ctx, io.LimitReader(source, asset.Size)}})
		source.Close()
		if err != nil {
			return err
		}
		if err := message(1, timestamp, &dashv2.Asset{Path: name, Segment: uint32(asset.Segment), Bytes: uint64(asset.Size), Attachment: name, ModifiedTimeNanoseconds: modified}); err != nil {
			return err
		}
	}
	for _, segment := range request.Segments {
		if path := logs[segment]; path != "" {
			if err := mcapLogTelemetry(ctx, writer, segment, path, clocks[segment], message); err != nil {
				return err
			}
		}
	}
	if err := e.mcapVideos(ctx, writer, job, assets, clocks, logs, message); err != nil {
		return err
	}
	return writer.Close()
}
