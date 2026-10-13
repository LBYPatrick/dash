package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"dash.local/backend/internal/telemetry"
	_ "modernc.org/sqlite"
)

var segmentPattern = regexp.MustCompile(`^([a-zA-Z0-9_-]+--[a-zA-Z0-9_-]+)--([0-9]+)$`)
var cameras = map[string]bool{"qcamera.ts": true, "fcamera.hevc": true, "ecamera.hevc": true, "dcamera.hevc": true}

type FileInfo struct {
	Duration float64 `json:"duration,omitempty"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Mtime    int64   `json:"mtime"`
}
type IndexedSegment struct {
	TelemetryVersion int                `json:"telemetryVersion,omitempty"`
	MediaVersion     int                `json:"mediaVersion,omitempty"`
	Metadata         telemetry.Metadata `json:"metadata"`
	Thumbnail        string             `json:"thumbnail,omitempty"`
	Files            []FileInfo         `json:"files"`
}
type IndexStatus struct {
	Running   bool   `json:"running"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Errors    int    `json:"errors"`
	Driving   string `json:"driving"`
}
type IndexSnapshot struct {
	Version  int                       `json:"version"`
	Status   IndexStatus               `json:"status"`
	Segments map[string]IndexedSegment `json:"segments"`
}
type Index struct {
	root, cache string
	db          *sql.DB
	state       StateReader
	mu          sync.Mutex
	status      IndexStatus
	generation  uint64
	cancel      context.CancelFunc
}

func newIndex(root, cache string, state StateReader) (*Index, error) {
	if err := os.MkdirAll(cache, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(cache, "index.sqlite3")
	open := func() (*sql.DB, error) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		var check string
		check = "ok"
		if state() == "parked" {
			ctx, cancel := parkedContext(context.Background(), state)
			err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check)
			cancel()
			if errors.Is(err, context.Canceled) {
				check = "ok"
				err = nil
			}
		}
		if err != nil || check != "ok" {
			db.Close()
			return nil, errors.New("corrupt index")
		}
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS segments (name TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, entry TEXT NOT NULL, summary TEXT NOT NULL)`)
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = db.Exec("SELECT summary FROM segments LIMIT 1"); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	db, err := open()
	if err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, removeErr
		}
		db, err = open()
	}
	if err != nil {
		return nil, err
	}
	return &Index{root: root, cache: cache, db: db, state: state}, nil
}
func (i *Index) snapshot() (IndexSnapshot, error) {
	i.mu.Lock()
	status := i.status
	i.mu.Unlock()
	status.Driving = i.state()
	result := IndexSnapshot{Version: 1, Status: status, Segments: map[string]IndexedSegment{}}
	rows, err := i.db.Query("SELECT name,summary FROM segments")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, data string
		if err := rows.Scan(&name, &data); err != nil {
			return result, err
		}
		var entry IndexedSegment
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			result.Status.Errors++
			continue
		}
		entry.Metadata.Points = nil
		entry.Metadata.Speeds = nil
		entry.Metadata.Engagement = nil
		result.Segments[name] = entry
	}
	return result, rows.Err()
}
func (i *Index) rebuild() error {
	if i.state() != "parked" {
		return errors.New("rebuild requires confirmed parked state")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.generation++
	if i.cancel != nil {
		i.cancel()
	}
	_, err := i.db.Exec("DELETE FROM segments")
	return err
}
func (i *Index) scan(parent context.Context) error {
	if i.state() != "parked" {
		return nil
	}
	ctx, cancel := parkedContext(parent, i.state)
	defer cancel()
	i.mu.Lock()
	if i.status.Running {
		i.mu.Unlock()
		return nil
	}
	i.status = IndexStatus{Running: true}
	generation := i.generation
	i.cancel = cancel
	i.mu.Unlock()
	defer func() { i.mu.Lock(); i.status.Running = false; i.mu.Unlock() }()
	folders, err := os.ReadDir(i.root)
	if err != nil {
		return err
	}
	sort.Slice(folders, func(a, b int) bool { return folders[a].Name() > folders[b].Name() })
	names := map[string]bool{}
	for _, folder := range folders {
		if folder.IsDir() && segmentPattern.MatchString(folder.Name()) {
			names[folder.Name()] = true
		}
	}
	i.mu.Lock()
	i.status.Total = len(names)
	i.mu.Unlock()
	for _, folder := range folders {
		if !names[folder.Name()] {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := i.scanSegment(ctx, folder.Name(), generation); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			i.mu.Lock()
			i.status.Errors++
			i.mu.Unlock()
		}
		i.mu.Lock()
		i.status.Completed++
		i.mu.Unlock()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	release, err := acquireCompute(ctx)
	if err != nil {
		return err
	}
	defer release()
	i.mu.Lock()
	defer i.mu.Unlock()
	if generation != i.generation {
		return nil
	}
	rows, err := i.db.QueryContext(ctx, "SELECT name FROM segments")
	if err != nil {
		return err
	}
	var obsolete []string
	for rows.Next() {
		if ctx.Err() != nil {
			rows.Close()
			return ctx.Err()
		}
		var name string
		if rows.Scan(&name) == nil && !names[name] {
			obsolete = append(obsolete, name)
		}
	}
	rows.Close()
	for _, name := range obsolete {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := i.db.ExecContext(ctx, "DELETE FROM segments WHERE name=?", name); err != nil {
			return err
		}
	}
	// Only keep thumbnails referenced by this index, after a complete scan.
	rows, err = i.db.QueryContext(ctx, "SELECT summary FROM segments")
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for rows.Next() {
		if ctx.Err() != nil {
			rows.Close()
			return ctx.Err()
		}
		var raw string
		var entry IndexedSegment
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &entry) == nil {
			used[filepath.Base(entry.Thumbnail)] = true
		}
	}
	rows.Close()
	cached, _ := filepath.Glob(filepath.Join(i.cache, "*.jpg"))
	for _, file := range cached {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !used[filepath.Base(file)] {
			_ = os.Remove(file)
		}
	}
	return nil
}
func (i *Index) scanSegment(ctx context.Context, name string, generation uint64) error {
	release, err := acquireCompute(ctx)
	if err != nil {
		return err
	}
	defer release()
	folder, err := safePath(i.root, name)
	if err != nil {
		return err
	}
	children, err := os.ReadDir(folder)
	if err != nil {
		return err
	}
	entry := IndexedSegment{Metadata: telemetry.Metadata{Status: "missing", Reason: "No qlog in this segment."}, Files: []FileInfo{}}
	for _, child := range children {
		if child.IsDir() {
			continue
		}
		path, err := safePath(i.root, filepath.Join(name, child.Name()))
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		entry.Files = append(entry.Files, FileInfo{Name: child.Name(), Size: info.Size(), Mtime: info.ModTime().UnixNano()})
	}
	sort.Slice(entry.Files, func(a, b int) bool { return entry.Files[a].Name < entry.Files[b].Name })
	inventory, _ := json.Marshal(entry.Files)
	hash := sha256.Sum256(inventory)
	fingerprint := hex.EncodeToString(hash[:])
	var oldHash, oldRaw string
	_ = i.db.QueryRow("SELECT fingerprint,entry FROM segments WHERE name=?", name).Scan(&oldHash, &oldRaw)
	var old IndexedSegment
	if oldHash == fingerprint && json.Unmarshal([]byte(oldRaw), &old) == nil && old.Metadata.Status != "error" && old.MediaVersion == 1 && old.TelemetryVersion == 3 {
		if old.Thumbnail == "" {
			return nil
		}
		if _, err := os.Stat(filepath.Join(i.cache, filepath.Base(old.Thumbnail))); err == nil {
			return nil
		}
	}
	entry.MediaVersion = 1
	entry.TelemetryVersion = 3
	for n, file := range entry.Files {
		if !cameras[file.Name] || file.Size > 256<<20 {
			continue
		}
		if oldHash == fingerprint && old.MediaVersion == 1 {
			for _, previous := range old.Files {
				if previous.Name == file.Name {
					entry.Files[n].Duration = previous.Duration
					break
				}
			}
			continue
		}
		duration, err := videoDuration(ctx, filepath.Join(folder, file.Name))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			entry.Files[n].Duration = duration
		}
	}
	var qlog, camera string
	for _, file := range entry.Files {
		if strings.HasPrefix(file.Name, "qlog") && (file.Name == "qlog" || file.Name == "qlog.zst" || file.Name == "qlog.bz2") {
			qlog = filepath.Join(folder, file.Name)
		}
		if cameras[file.Name] && (camera == "" || file.Name == "qcamera.ts") {
			camera = filepath.Join(folder, file.Name)
		}
	}
	if qlog != "" {
		meta, err := telemetry.Decode(ctx, qlog)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entry.Metadata = telemetry.Metadata{Status: "error", Reason: err.Error()}
		} else {
			entry.Metadata = meta
		}
	}
	if camera != "" {
		digest := sha256.Sum256([]byte(name + fingerprint))
		thumbnail := fmt.Sprintf("%x.jpg", digest)
		target := filepath.Join(i.cache, thumbnail)
		job, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := runFFmpeg(job, "-i", camera, "-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "5", target)
		cancel()
		if err == nil {
			entry.Thumbnail = "/api/thumbnail/" + thumbnail
		} else {
			_ = os.Remove(target)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	entry.Metadata.Points = nil
	entry.Metadata.Speeds = nil
	entry.Metadata.Engagement = nil
	summary, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if generation != i.generation {
		return context.Canceled
	}
	_, err = i.db.Exec("INSERT OR REPLACE INTO segments(name,fingerprint,entry,summary) VALUES(?,?,?,?)", name, fingerprint, string(data), string(summary))
	return err
}

func (i *Index) driveMetadata(drive string) (map[string]IndexedSegment, error) {
	if !segmentPattern.MatchString(drive + "--0") {
		return nil, errors.New("invalid drive")
	}
	rows, err := i.db.Query("SELECT name,entry FROM segments WHERE substr(name,1,?) = ?", len(drive)+2, drive+"--")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]IndexedSegment{}
	count := 0
	for rows.Next() {
		var name, raw string
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 2<<20 {
			return nil, errors.New("metadata exceeds per-segment budget")
		}
		var entry IndexedSegment
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, err
		}
		if len(entry.Metadata.Points) > 0 || len(entry.Metadata.Speeds) > 0 || len(entry.Metadata.Engagement) > 0 {
			count += len(entry.Metadata.Points) + len(entry.Metadata.Speeds) + len(entry.Metadata.Engagement)
		}
		if count > 50000 {
			return nil, errors.New("drive metadata exceeds interactive sample budget")
		}
		result[name] = entry
	}
	return result, rows.Err()
}
