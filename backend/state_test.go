package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFirmwarePublishedState(t *testing.T) {
	for _, test := range []struct{ name, on, off, want string }{
		{"both missing", "missing", "missing", "unknown"},
		{"published offroad", "missing", "1", "parked"},
		{"published onroad", "missing", "0", "onroad"},
		{"legacy offroad", "0", "1", "parked"},
		{"legacy onroad veto", "1", "1", "onroad"},
		{"missing required offroad", "0", "missing", "unknown"},
		{"unreadable optional onroad", "denied", "1", "unknown"},
		{"unreadable required offroad", "0", "denied", "unknown"},
		{"malformed onroad", "bad", "1", "unknown"},
		{"malformed offroad", "0", "bad", "unknown"},
		{"empty offroad", "missing", "", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			read := func(name string) ([]byte, error) {
				value := test.off
				if name == "IsOnroad" {
					value = test.on
				}
				switch value {
				case "missing":
					return nil, os.ErrNotExist
				case "denied":
					return nil, os.ErrPermission
				}
				return []byte(value), nil
			}
			if got := firmwareState(read); got != test.want {
				t.Fatalf("state=%s, want %s", got, test.want)
			}
		})
	}
}

func TestFirmwareStateRechecksParkedTransitions(t *testing.T) {
	for _, test := range []struct{ name, key, first, second, want string }{
		{"legacy ignition", "IsOnroad", "0", "1", "onroad"},
		{"published ignition", "IsOffroad", "1", "0", "onroad"},
		{"offroad disappears", "IsOffroad", "1", "missing", "unknown"},
		{"legacy key disappears", "IsOnroad", "0", "missing", "unknown"},
		{"legacy key appears", "IsOnroad", "missing", "0", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			state := firmwareState(func(key string) ([]byte, error) {
				value := "1"
				if key == "IsOnroad" {
					value = "0"
				}
				if key == test.key {
					reads++
					value = test.first
					if reads > 1 {
						value = test.second
					}
				}
				if value == "missing" {
					return nil, os.ErrNotExist
				}
				return []byte(value), nil
			})
			if state != test.want {
				t.Fatalf("state=%s, want %s", state, test.want)
			}
		})
	}
}

func TestOffroadOnlyParamsStopAndResumeHeavyContext(t *testing.T) {
	directory := t.TempDir()
	off := filepath.Join(directory, "IsOffroad")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(off, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := paramsState(directory)
	for _, next := range []string{"0", "missing", "bad"} {
		write("1")
		ctx, cancel := parkedContext(context.Background(), state)
		if ctx.Err() != nil {
			cancel()
			t.Fatal("confirmed offroad did not permit work")
		}
		if next == "missing" {
			if err := os.Remove(off); err != nil {
				t.Fatal(err)
			}
		} else {
			write(next)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * DrivingWatchInterval):
			cancel()
			t.Fatal("heavy context survived non-offroad state")
		}
		cancel()
		blocked, stop := parkedContext(context.Background(), state)
		if !errors.Is(blocked.Err(), context.Canceled) {
			stop()
			t.Fatal("new heavy work permitted")
		}
		stop()
	}
	// A broken optional legacy flag is an error, rather than a missing key.
	write("1")
	if err := os.Symlink(filepath.Join(directory, "absent"), filepath.Join(directory, "IsOnroad")); err != nil {
		t.Fatal(err)
	}
	if state() != "unknown" {
		t.Fatal("broken legacy flag permitted heavy work")
	}
}

func TestExportCleanupStopsWhenOffroadEnds(t *testing.T) {
	for _, next := range []string{"onroad", "unknown"} {
		t.Run(next, func(t *testing.T) {
			folder := t.TempDir()
			output := filepath.Join(folder, "finished.zip")
			if err := os.WriteFile(output, []byte("complete"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			index := &Index{state: func() string {
				calls++
				if calls > 2 {
					return next
				}
				return "parked"
			}}
			job := &ExportJob{ID: "saved", Status: "complete", Created: time.Now().Add(-2 * time.Hour).Unix(), output: output}
			manager := &ExportManager{index: index, folder: folder, jobs: map[string]*ExportJob{"saved": job}}
			manager.cleanup()
			if _, err := os.Stat(output); err != nil {
				t.Fatal("cleanup removed data after leaving offroad", err)
			}
			if manager.jobs["saved"] == nil {
				t.Fatal("cleanup changed jobs after leaving offroad")
			}
		})
	}
}
