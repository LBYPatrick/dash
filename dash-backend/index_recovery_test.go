package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Optional physical execution still checks real ignition and uses the fixed
// device budget. All fixture files and killed processes belong to the test.
func indexCrashTestState(t *testing.T) StateReader {
	t.Helper()
	if os.Getenv("DASH_INDEX_CRASH_TEST_DEVICE") == "1" {
		deviceMode = true
		runtime.GOMAXPROCS(DeviceCPUCores)
		debug.SetMemoryLimit(GoHeapTargetMiB << 20)
		state := paramsState("/data/params/d")
		if state() != "parked" {
			t.Fatal("crash test requires actual confirmed parked state")
		}
		return state
	}
	return func() string { return "parked" }
}

func TestIndexCrashWriter(t *testing.T) {
	home := os.Getenv("DASH_INDEX_CRASH_TEST_HOME")
	if home == "" {
		t.Skip("subprocess helper")
	}
	state := indexCrashTestState(t)
	index, err := newIndex(filepath.Join(home, "source"), filepath.Join(home, "cache"), state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := parkedContext(context.Background(), state)
	defer cancel()
	tx, err := index.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Force pages to spill from SQLite's bounded cache into an uncommitted WAL.
	if _, err = tx.Exec("UPDATE segments SET entry=?,fingerprint='uncommitted'", strings.Repeat("x", 6<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO segments(name,fingerprint,entry,summary) VALUES('pending','pending','pending','pending')"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Hour)
}

func TestIndexRecoversKilledWriterWithoutLosingCommittedParts(t *testing.T) {
	state := indexCrashTestState(t)
	index, _ := fixtureIndex(t)
	index.state = state
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := index.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	cache, root := index.cache, index.root
	var mode string
	var synchronous int
	index.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	index.db.QueryRow("PRAGMA synchronous").Scan(&synchronous)
	if mode != "wal" || synchronous != 2 {
		t.Fatal("index durability settings", mode, synchronous)
	}
	index.db.Close()
	home := t.TempDir()
	// The helper accepts only its own test directory; symlinks point at this
	// test's synthetic recordings/cache, never a physical device or firmware.
	if err := os.Symlink(cache, filepath.Join(home, "cache")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(home, "source")); err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	command := exec.Command(executable, "-test.run=^TestIndexCrashWriter$", "-test.timeout=30s")
	command.Env = append(os.Environ(), "DASH_INDEX_CRASH_TEST_HOME="+home)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			command.Process.Kill()
			command.Wait()
			t.Fatal("writer did not reach interrupted transaction", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info, err := os.Stat(filepath.Join(cache, "index.sqlite3-wal")); err != nil || info.Size() == 0 {
		t.Fatal("test did not produce a hot WAL", err)
	}
	command.Process.Kill()
	command.Wait()
	recovered, err := newIndex(root, cache, state)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.db.Close()
	after, err := recovered.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before.Segments)
	b, _ := json.Marshal(after.Segments)
	if !bytes.Equal(a, b) {
		t.Fatal("committed index changed after crash recovery")
	}
	var integrity string
	recovered.db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if integrity != "ok" {
		t.Fatal("recovered index is corrupt", integrity)
	}
	if _, err := os.Stat(filepath.Join(cache, ".index-corrupt")); !os.IsNotExist(err) {
		t.Fatal("recoverable WAL was discarded")
	}
	if err := recovered.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var writes int
	recovered.db.QueryRow("SELECT total_changes()").Scan(&writes)
	if writes != 0 {
		t.Fatal("restart rebuilt an already committed part", writes)
	}
}

func TestIndexWorkersStopBeforeRoadAndResume(t *testing.T) {
	for _, next := range []string{"onroad", "unknown"} {
		t.Run(next, func(t *testing.T) {
			index, _ := fixtureIndex(t)
			var state atomic.Value
			state.Store("parked")
			index.state = func() string { return state.Load().(string) }
			data, _ := os.ReadFile(filepath.Join(index.root, fixtureDrive+"--0", "qlog.bz2"))
			folder := filepath.Join(index.root, fixtureDrive+"--1")
			os.MkdirAll(folder, 0700)
			os.WriteFile(filepath.Join(folder, "qlog.bz2"), data, 0600)
			release, err := acquireCompute(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- index.scan(context.Background()) }()
			time.Sleep(30 * time.Millisecond)
			state.Store(next)
			select {
			case err := <-done:
				if err == nil {
					t.Error("active work ignored driving gate")
				}
			case <-time.After(time.Second):
				release()
				t.Fatal("index workers did not stop")
			}
			release()
			snapshot, _ := index.snapshot()
			if len(snapshot.Segments) != 0 {
				t.Fatal("non-parked work was committed")
			}
			state.Store("parked")
			if err := index.scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			snapshot, _ = index.snapshot()
			if len(snapshot.Segments) != 2 {
				t.Fatal("parked scan did not resume unfinished parts")
			}
		})
	}
}

func TestIndexThumbnailCancellationAndOrphanRecovery(t *testing.T) {
	index, _ := fixtureIndex(t)
	target := filepath.Join(index.cache, "committed.jpg")
	os.WriteFile(target, []byte("committed"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeIndexThumbnail(ctx, "unused", target); err == nil {
		t.Fatal("canceled thumbnail job succeeded")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "committed" {
		t.Fatal("partial image replaced a committed thumbnail")
	}
	orphan := filepath.Join(index.cache, ".index-thumb-interrupted.jpg")
	os.WriteFile(orphan, []byte("partial"), 0600)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("interrupted thumbnail was not cleaned")
	}
}

func TestBusyIndexIsNeverQuarantined(t *testing.T) {
	index, _ := fixtureIndex(t)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := index.db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	if _, err := index.db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer index.db.Exec("ROLLBACK")
	reopened, err := newIndex(index.root, index.cache, func() string { return "parked" })
	if err == nil {
		reopened.db.Close()
		t.Fatal("exclusive database lock should refuse opening")
	}
	if _, err := os.Stat(filepath.Join(index.cache, ".index-corrupt")); !os.IsNotExist(err) {
		t.Fatal("busy database was treated as corrupt")
	}
	if _, err := os.Stat(filepath.Join(index.cache, "index.sqlite3")); err != nil {
		t.Fatal("busy index was removed", err)
	}
}

func TestActiveIndexMediaStopsOnRoadOrUnknown(t *testing.T) {
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	sample := cameraFixture(t, "h264", false)
	for _, next := range []string{"onroad", "unknown"} {
		t.Run(next, func(t *testing.T) {
			index, _ := fixtureIndex(t)
			for _, part := range []string{"0", "1"} {
				folder := filepath.Join(index.root, fixtureDrive+"--"+part)
				os.MkdirAll(folder, 0700)
				os.WriteFile(filepath.Join(folder, "qcamera.ts"), sample, 0600)
			}
			var state atomic.Value
			state.Store("parked")
			index.state = func() string { return state.Load().(string) }
			wrapper := t.TempDir()
			marker := filepath.Join(wrapper, "started")
			script := "#!/bin/bash\nset -e\nargs=(\"$@\")\nlast=$((${#args[@]}-1))\necho started > \"$DASH_TEST_INDEX_STARTED\"\nexec \"$DASH_TEST_INDEX_FFMPEG\" -stream_loop -1 -re \"${args[@]:0:$last}\" -ss 60 \"${args[$last]}\"\n"
			os.WriteFile(filepath.Join(wrapper, "ffmpeg"), []byte(script), 0700)
			t.Setenv("DASH_TEST_INDEX_STARTED", marker)
			t.Setenv("DASH_TEST_INDEX_FFMPEG", real)
			t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
			done := make(chan error, 1)
			go func() { done <- index.scan(context.Background()) }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					state.Store(next)
					t.Fatal("index encoder did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			started := time.Now()
			state.Store(next)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("active index ignored driving state")
				}
			case <-time.After(time.Second):
				t.Fatal("index encoder not canceled")
			}
			if time.Since(started) > time.Second || len(computeSlot) != 0 {
				t.Fatal("index media budget was not released")
			}
			snapshot, _ := index.snapshot()
			if len(snapshot.Segments) != 0 {
				t.Fatal("interrupted media part was published as ready")
			}
		})
	}
}

func TestMissingThumbnailRepairsDamagedCachedMetadata(t *testing.T) {
	index, _ := fixtureIndex(t)
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := index.snapshot()
	entry := snapshot.Segments[fixtureDrive+"--0"]
	entry.Thumbnail = "/api/thumbnail/missing.jpg"
	raw, _ := json.Marshal(entry)
	index.db.Exec("UPDATE segments SET entry='broken cached JSON',summary=?", string(raw))
	if err := index.scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	details, err := index.driveMetadata(fixtureDrive)
	if err != nil || details[fixtureDrive+"--0"].Metadata.Status != "ready" {
		t.Fatal("cached metadata was not repaired", err)
	}
}
