package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
)

var previewFilePattern = regexp.MustCompile(`^[a-f0-9]{64}\.(ts|mp4)(\.partial\.(ts|mp4))?$`)

// Remove only the old derived preview media after native indexing completes.
// Metadata, packet indexes, symlinks and unknown files remain untouched.
func removeLegacyPreviews(ctx context.Context, folder string) {
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() {
		return
	}
	entries, _ := os.ReadDir(folder)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if !previewFilePattern.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		_ = os.Remove(filepath.Join(folder, entry.Name()))
	}
}
