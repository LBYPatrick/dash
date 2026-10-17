package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var previewFilePattern = regexp.MustCompile(`^[a-f0-9]{64}\.(ts|mp4)(\.partial\.(ts|mp4))?$`)

// trimPreviewCache only evicts derived video files. Packet indexes, metadata,
// unknown files and directories remain intact; source recordings live elsewhere.
func trimPreviewCache(folder string, limit int64, enoughSpace func() bool) {
	entries, _ := os.ReadDir(folder)
	var files []os.FileInfo
	var total int64
	for _, entry := range entries {
		if !previewFilePattern.MatchString(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			files = append(files, info)
			total += info.Size()
		}
	}
	sort.Slice(files, func(a, b int) bool { return files[a].ModTime().Before(files[b].ModTime()) })
	for _, info := range files {
		if total <= limit && (enoughSpace == nil || enoughSpace()) {
			return
		}
		if os.Remove(filepath.Join(folder, info.Name())) == nil {
			total -= info.Size()
		}
	}
}

// ensurePreviewSpace reclaims old previews before reporting storage exhaustion.
// Callers hold the parked compute lease, so no other preview writer is active.
func ensurePreviewSpace(folder string, required int64) error {
	if err := requireRecordingSpace(folder, required); !errors.Is(err, errRecordingReserve) {
		return err
	}
	trimPreviewCache(folder, 256<<20, func() bool { return requireRecordingSpace(folder, required) == nil })
	return requireRecordingSpace(folder, required)
}
