package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const logFileLimit = 2 << 20
const diagnosticTailLimit = 512 << 10

// Diagnostics retains bounded application logs across service restarts. The
// journal remains the lifecycle log; this copy is readable by the web service.
type Diagnostics struct {
	mu        sync.Mutex
	directory string
	file      *os.File
	size      int64
	started   time.Time
}

func newDiagnostics(directory string) (*Diagnostics, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "backend.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return &Diagnostics{directory: directory, file: file, size: info.Size(), started: time.Now()}, nil
}
func (d *Diagnostics) Write(data []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	original := len(data)
	if len(data) > 16<<10 {
		data = append(append([]byte{}, data[:16<<10]...), []byte(" [truncated]\n")...)
	}
	if d.size+int64(len(data)) > logFileLimit {
		if err := d.file.Close(); err != nil {
			return 0, err
		}
		_ = os.Remove(filepath.Join(d.directory, "backend.log.2"))
		_ = os.Rename(filepath.Join(d.directory, "backend.log.1"), filepath.Join(d.directory, "backend.log.2"))
		if err := os.Rename(filepath.Join(d.directory, "backend.log"), filepath.Join(d.directory, "backend.log.1")); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(filepath.Join(d.directory, "backend.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return 0, err
		}
		d.file = file
		d.size = 0
	}
	n, err := d.file.Write(data)
	d.size += int64(n)
	if err != nil {
		return n, err
	}
	return original, nil
}
func (d *Diagnostics) Close() error { d.mu.Lock(); defer d.mu.Unlock(); return d.file.Close() }
func (d *Diagnostics) recent() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var result strings.Builder
	for _, name := range []string{"backend.log.1", "backend.log"} {
		file, err := os.Open(filepath.Join(d.directory, name))
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}
		offset := max(int64(0), info.Size()-diagnosticTailLimit)
		_, _ = file.Seek(offset, io.SeekStart)
		data, _ := io.ReadAll(io.LimitReader(file, diagnosticTailLimit))
		file.Close()
		if offset > 0 {
			if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
				data = data[newline+1:]
			}
		}
		fmt.Fprintf(&result, "\n--- %s (recent tail) ---\n%s", name, data)
	}
	return result.String()
}
func (s *Server) downloadDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.diagnostics == nil {
		fail(w, 503, fmt.Errorf("diagnostic logging unavailable"))
		return
	}
	var result strings.Builder
	fmt.Fprintln(&result, "Dash backend diagnostics v1")
	fmt.Fprintf(&result, "Dash build: %s channel=%s revision=%s\n", buildVersion, buildChannel, buildRevision)
	if s.updates != nil {
		update := s.updates.snapshot()
		fmt.Fprintf(&result, "Updates: managed=%t channel=%s phase=%s message=%s\n", update.Managed, update.Channel, update.Phase, update.Message)
	}
	fmt.Fprintf(&result, "Generated: %s\nStarted: %s\nRuntime: %s %s/%s\nDriving state: %s\nSimulation: %t\nCompute slot occupied: %t\n", time.Now().UTC().Format(time.RFC3339), s.diagnostics.started.UTC().Format(time.RFC3339), runtime.Version(), runtime.GOOS, runtime.GOARCH, s.index.state(), s.simulation, len(computeSlot) > 0)
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				fmt.Fprintf(&result, "%s: %s\n", setting.Key, setting.Value)
			}
		}
	}
	s.index.mu.Lock()
	status := s.index.status
	s.index.mu.Unlock()
	fmt.Fprintf(&result, "Index: running=%t completed=%d total=%d errors=%d\nManaged live API: %t\nFixed policy: cores=%d mediaThreads=%d heavyJobs=%d GoHeapMiB=%d indexWorkers=%d indexThreads=%d\n", status.Running, status.Completed, status.Total, status.Errors, s.managedLive(), DeviceCPUCores, MediaThreads, HeavyJobLimit, GoHeapTargetMiB, IndexWorkers, IndexMediaThreads)
	if !deviceMode {
		fmt.Fprintf(&result, "Process RSS KiB: %d\nSystem available RAM KiB: %d\n", procKB("/proc/self/status", "VmRSS:"), procKB("/proc/meminfo", "MemAvailable:"))
		directory, err := currentCgroup()
		if err == nil {
			for _, name := range []string{"cpu.max", "memory.max", "memory.high", "memory.swap.max", "memory.current", "memory.events", "pids.max", "pids.current"} {
				data, err := os.ReadFile(filepath.Join(directory, name))
				if err == nil && len(data) < 4096 {
					fmt.Fprintf(&result, "%s: %s\n", name, strings.TrimSpace(string(data)))
				}
			}
		} else {
			fmt.Fprintf(&result, "Cgroup: unavailable (%s)\n", err)
		}
	} else {
		fmt.Fprintln(&result, "Device mode: system resource probing disabled; budgets are configured in policy.go.")
	}
	fmt.Fprintln(&result, "\nLogs may contain recording names and error paths. Review before sharing. No video, GPS samples, request bodies, credentials, or environment-variable dump is included.")
	result.WriteString(s.diagnostics.recent())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="dash-diagnostics-`+time.Now().UTC().Format("20060102T150405Z")+`.txt"`)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, result.String())
	}
}

func setupLogging(cache string) (*Diagnostics, error) {
	diagnostics, err := newDiagnostics(filepath.Join(cache, "logs"))
	if err != nil {
		return nil, err
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.SetOutput(io.MultiWriter(os.Stderr, diagnostics))
	return diagnostics, nil
}
