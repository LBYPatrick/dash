package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"
	"time"
)

// Opt-in, uses only a disposable Dash cache and a caller-provided recording
// root. Device runs still use the actual firmware driving gate and fixed budget.
func TestIndexDeviceBenchmark(t *testing.T) {
	root := os.Getenv("DASH_INDEX_BENCH_ROOT")
	if root == "" {
		t.Skip("opt-in indexing benchmark")
	}
	home := os.Getenv("DASH_INDEX_BENCH_HOME")
	if home == "" {
		t.Fatal("disposable benchmark home required")
	}
	deviceMode = os.Getenv("DASH_INDEX_BENCH_DEVICE") == "1"
	runtime.GOMAXPROCS(DeviceCPUCores)
	debug.SetMemoryLimit(GoHeapTargetMiB << 20)
	log.SetOutput(io.Discard)
	state := paramsState(os.Getenv("DASH_INDEX_BENCH_PARAMS"))
	run := func(n int) float64 {
		if state() != "parked" {
			t.Fatal("benchmark stopped: driving state not confirmed parked")
		}
		cache := filepath.Join(home, strconv.Itoa(n))
		os.RemoveAll(cache)
		started := time.Now()
		index, err := newIndex(root, cache, state)
		if err != nil {
			t.Fatal(err)
		}
		err = index.scan(context.Background())
		if n == -1 && err == nil {
			rows, queryErr := index.db.Query("SELECT name,entry FROM segments")
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			entries := map[string]json.RawMessage{}
			for rows.Next() {
				var name, raw string
				rows.Scan(&name, &raw)
				entries[name] = json.RawMessage(raw)
			}
			rows.Close()
			raw, _ := json.Marshal(entries)
			os.WriteFile(filepath.Join(home, "warmup-entries.json"), raw, 0600)
		}
		index.db.Close()
		if err != nil {
			t.Fatal(err)
		}
		ms := float64(time.Since(started)) / float64(time.Millisecond)
		os.RemoveAll(cache)
		return ms
	}
	warmup := run(-1)
	trials := max(3, min(100, int(math.Floor(280000/warmup))))
	if fixed := os.Getenv("DASH_INDEX_BENCH_TRIALS"); fixed != "" {
		trials, _ = strconv.Atoi(fixed)
	}
	samples := make([]float64, 0, trials)
	for n := 0; n < trials; n++ {
		samples = append(samples, run(n))
	}
	result := map[string]any{"warmupMs": warmup, "trials": trials, "milliseconds": samples, "go": runtime.Version(), "arch": runtime.GOARCH, "cores": DeviceCPUCores}
	raw, _ := json.Marshal(result)
	if err := os.WriteFile(filepath.Join(home, "results.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("cold index: %d trials; warmup %.1f ms", trials, warmup)
}
