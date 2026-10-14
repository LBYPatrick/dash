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

	"github.com/foxglove/mcap/go/mcap"
	"golang.org/x/sys/unix"
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
		fmt.Fprintf(&lines, "file '%s'\n", strings.ReplaceAll(asset.Path, "'", "'\\''"))
	}
	if err := os.WriteFile(listing, []byte(lines.String()), 0600); err != nil {
		return err
	}
	args := []string{"-fflags", "+genpts", "-f", "concat", "-safe", "0", "-i", listing, "-map", "0:v:0", "-an"}
	if strings.HasSuffix(request.Camera, ".hevc") {
		args = append([]string{"-r", "20"}, args...)
	}
	if request.H264 {
		args = append(args, "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-crf", "23", "-pix_fmt", "yuv420p")
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

var dashSchemaFields = map[string]map[string]string{
	"Asset":        {"path": "string", "segment": "integer", "bytes": "integer", "attachment": "string"},
	"Position":     {"latitude": "number", "longitude": "number", "altitude": "number", "speed": "number", "time": "number", "monotonic": "number"},
	"VehicleState": {"time": "number", "speed": "number", "steeringAngle": "number", "brakePressed": "boolean", "gasPressed": "boolean"},
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
	if err := writer.WriteHeader(&mcap.Header{Profile: "dash.v1", Library: "dash-backend/0.1"}); err != nil {
		return err
	}
	names := []string{"Asset", "Position", "VehicleState"}
	for n, name := range names {
		properties := map[string]any{}
		for field, kind := range dashSchemaFields[name] {
			properties[field] = map[string]string{"type": kind}
		}
		data, _ := json.Marshal(map[string]any{"$schema": "http://json-schema.org/draft-07/schema#", "title": "dash.v1." + name, "type": "object", "properties": properties})
		id := uint16(n + 1)
		if err := writer.WriteSchema(&mcap.Schema{ID: id, Name: "dash.v1." + name, Encoding: "jsonschema", Data: data}); err != nil {
			return err
		}
		if err := writer.WriteChannel(&mcap.Channel{ID: id, SchemaID: id, Topic: "/dash/" + name, MessageEncoding: "json"}); err != nil {
			return err
		}
	}
	if err := writer.WriteMetadata(&mcap.Metadata{Name: "dash.drive", Metadata: map[string]string{"id": request.Drive, "recordings": "Camera frames use foxglove.CompressedVideo; original videos and logs are also retained as attachments.", "timeBasis": "GPS measured UTC; vehicle monotonic timestamps mapped to GPS. Video uses segment-start GPS alignment plus presentation offsets (raw HEVC at 20 fps), not exact exposure timestamps. Without GPS uses file timestamp estimates."}}); err != nil {
		return err
	}
	sequence := uint32(0)
	message := func(channel uint16, timestamp uint64, value any) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		sequence++
		return writer.WriteMessage(&mcap.Message{ChannelID: channel, Sequence: sequence, LogTime: timestamp, PublishTime: timestamp, Data: data})
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
		timestamp := uint64(max(int64(0), asset.Modified.UnixNano()))
		err = writer.WriteAttachment(&mcap.Attachment{Name: name, LogTime: timestamp, CreateTime: timestamp, MediaType: "application/octet-stream", DataSize: uint64(asset.Size), Data: checkedReader{ctx, io.LimitReader(source, asset.Size)}})
		source.Close()
		if err != nil {
			return err
		}
		if err := message(1, timestamp, map[string]any{"path": name, "segment": asset.Segment, "bytes": asset.Size, "attachment": name}); err != nil {
			return err
		}
	}
	snapshot, err := e.index.driveMetadata(request.Drive)
	if err != nil {
		return err
	}
	for _, segment := range request.Segments {
		entry := snapshot[fmt.Sprintf("%s--%d", request.Drive, segment)].Metadata
		for _, point := range entry.Points {
			if err := message(2, uint64(max(0, point.Time*1e9)), point); err != nil {
				return err
			}
		}
		first := float64(0)
		if len(entry.Speeds) > 0 {
			first = entry.Speeds[0].Time
		}
		estimate := float64(0)
		for _, asset := range assets {
			if asset.Segment == segment {
				estimate = float64(asset.Modified.UnixNano())/1e9 - entry.Duration
				break
			}
		}
		for _, sample := range entry.Speeds {
			utc := estimate + sample.Time - first
			if len(entry.Points) > 0 {
				anchor := entry.Points[0]
				utc = anchor.Time + sample.Time - anchor.Monotonic
			}
			if err := message(3, uint64(max(0, utc*1e9)), sample); err != nil {
				return err
			}
		}
	}
	if err := e.mcapVideos(ctx, writer, job, request, assets, snapshot, message); err != nil {
		return err
	}
	return writer.Close()
}
