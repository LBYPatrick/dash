package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var immutableAssetPattern = regexp.MustCompile(`^assets/[A-Za-z0-9_.-]+-[A-Za-z0-9_-]{8,}\.(css|js|mjs|woff2?|png|svg)$`)

func (s *Server) retainedAsset(path string) (string, error) {
	if !immutableAssetPattern.MatchString(path) || s.updates == nil || s.updates.home == "" {
		return "", os.ErrNotExist
	}
	root := filepath.Join(s.updates.home, "releases")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() > entries[b].Name() })
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() || !releaseIDPattern.MatchString(entry.Name()) {
			continue
		}
		checked++
		if checked > 32 {
			break
		}
		file, err := safePath(filepath.Join(root, entry.Name(), "dist"), path)
		if err != nil {
			continue
		}
		info, err := os.Stat(file)
		if err == nil && info.Mode().IsRegular() && info.Size() <= 8<<20 {
			return file, nil
		}
	}
	return "", errors.New("asset unavailable in retained releases")
}
