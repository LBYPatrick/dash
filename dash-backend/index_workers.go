package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Lazy acquisition keeps unchanged inventory checks out of the media queue.
// The slot is released only after every worker has stopped, including on ignition.
type indexComputeBatch struct {
	ctx     context.Context
	mu      sync.Mutex
	release func()
}

func (b *indexComputeBatch) acquire() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.release != nil {
		return b.ctx.Err()
	}
	release, err := acquireCompute(b.ctx)
	if err == nil {
		b.release = release
	}
	return err
}
func (b *indexComputeBatch) close() {
	if b.release != nil {
		b.release()
	}
}

func recordingFingerprint(folder string) (string, error) {
	children, err := os.ReadDir(folder)
	if err != nil {
		return "", err
	}
	files := []FileInfo{}
	for _, child := range children {
		if child.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(folder, child.Name()))
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() {
			files = append(files, FileInfo{Name: child.Name(), Size: info.Size(), Mtime: info.ModTime().UnixNano()})
		}
	}
	raw, _ := json.Marshal(files)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// Never expose an in-progress JPEG under the committed thumbnail name. A crash
// can leave only an unreferenced temp file; the next completed scan removes it.
func writeIndexThumbnail(ctx context.Context, camera, target string) error {
	file, err := os.CreateTemp(filepath.Dir(target), ".index-thumb-*.jpg")
	if err != nil {
		return err
	}
	temporary := file.Name()
	file.Close()
	defer os.Remove(temporary)
	err = runFFmpeg(ctx, "-threads", strconv.Itoa(IndexMediaThreads), "-filter_threads", "1", "-i", camera, "-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "5", "-threads", "1", temporary)
	if err != nil {
		return err
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temporary, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}
