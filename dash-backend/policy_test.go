package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeGateCannotOverrideFirmware(t *testing.T) {
	for _, actual := range []string{"parked", "onroad", "unknown"} {
		for _, gate := range []string{"parked", "onroad", "unknown"} {
			got := combinedState(func() string { return actual }, func() string { return gate })()
			want := actual
			if actual == "parked" {
				want = gate
			}
			if got != want {
				t.Fatalf("%s/%s: %s", actual, gate, got)
			}
		}
	}
}

func TestDevicePolicyAndStateCancellation(t *testing.T) {
	if IndexWorkers < 1 || IndexMediaThreads < 1 || IndexWorkers*IndexMediaThreads > MediaThreads {
		t.Fatal("index worker budget exceeds media budget")
	}
	if DeviceCPUCores <= 0 || MediaThreads <= 0 || MediaThreads > DeviceCPUCores || HeavyJobLimit != 1 || GoHeapTargetMiB >= ServiceMemoryHighMiB || ServiceMemoryHighMiB > ServiceMemoryMaxMiB {
		t.Fatal("inconsistent fixed policy")
	}
	directory := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("IsOnroad", "0")
	write("IsOffroad", "1")
	for _, value := range []string{"1", "unknown"} {
		write("IsOnroad", "0")
		ctx, cancel := parkedContext(context.Background(), paramsState(directory))
		write("IsOnroad", value)
		select {
		case <-ctx.Done():
		case <-time.After(5 * DrivingWatchInterval):
			t.Fatal("worker context survived non-parked state")
		}
		cancel()
	}
}
