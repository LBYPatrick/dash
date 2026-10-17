package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreviewEvictionReclaimsOldestWithoutDiscardingIndexes(t *testing.T) {
	folder := t.TempDir()
	old := strings.Repeat("a", 64) + ".ts"
	recent := strings.Repeat("b", 64) + ".mp4"
	for _, name := range []string{old, recent, "packet.seek.json", "unknown.ts"} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte("keep bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(filepath.Join(folder, old), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(folder, strings.Repeat("c", 64)+".ts")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	trimPreviewCache(folder, 1000, func() bool { _, err := os.Stat(filepath.Join(folder, old)); return os.IsNotExist(err) })
	for _, name := range []string{recent, "packet.seek.json", "unknown.ts"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatal("unrelated or useful cache removed", name, err)
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("followed or deleted a link", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "original" {
		t.Fatal("source modified", err)
	}
}
