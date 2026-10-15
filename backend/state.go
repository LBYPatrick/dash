package main

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type StateReader func() string

// IsOffroad is the published firmware state. Older firmware also publishes
// IsOnroad; an available onroad flag always vetoes heavy work. Read permission
// errors and malformed flags must never be treated as an absent optional key.
func paramsState(directory string) StateReader {
	return func() string {
		return firmwareState(func(name string) ([]byte, error) {
			path := filepath.Join(directory, name)
			data, err := os.ReadFile(path)
			if os.IsNotExist(err) {
				if _, statErr := os.Lstat(path); statErr == nil || !os.IsNotExist(statErr) {
					return nil, errors.New("unreadable firmware state")
				}
			}
			return data, err
		})
	}
}

func firmwareState(read func(string) ([]byte, error)) string {
	readFlag := func(name string) (string, bool) {
		data, err := read(name)
		if os.IsNotExist(err) && name == "IsOnroad" {
			return "absent", true
		}
		value := strings.TrimSpace(string(data))
		return value, err == nil && (value == "0" || value == "1")
	}
	on, ok := readFlag("IsOnroad")
	if !ok {
		return "unknown"
	}
	if on == "1" {
		return "onroad"
	}
	off, ok := readFlag("IsOffroad")
	if !ok {
		return "unknown"
	}
	if off == "0" {
		return "onroad"
	}
	// Parameter updates are separate atomic file replacements. Confirm the
	// same parked observation before allowing work across a state transition.
	onAgain, ok := readFlag("IsOnroad")
	if !ok {
		return "unknown"
	}
	if onAgain == "1" {
		return "onroad"
	}
	if onAgain != on {
		return "unknown"
	}
	offAgain, ok := readFlag("IsOffroad")
	if !ok {
		return "unknown"
	}
	if offAgain == "0" {
		return "onroad"
	}
	return "parked"
}
func parkedContext(parent context.Context, state StateReader) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if state() != "parked" {
		cancel()
		return ctx, cancel
	}
	go func() {
		ticker := time.NewTicker(DrivingWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if state() != "parked" || !memoryAvailable() {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}
func runFFmpeg(ctx context.Context, args ...string) error {
	return runMediaCommand(ctx, "ffmpeg", nil, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-max_alloc", strconv.Itoa(MediaAllocationLimitMiB << 20), "-threads", strconv.Itoa(MediaThreads), "-filter_threads", strconv.Itoa(MediaThreads)}, args...)...)
}
func runMediaCommand(ctx context.Context, binary string, stdout io.Writer, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tool, err := mediaTool(binary)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, tool, args...)
	command.Stdout = stdout
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	started := time.Now()
	log.Printf("%s started pid=%d", binary, command.Process.Pid)
	defer func() {
		log.Printf("%s finished pid=%d duration=%s canceled=%t", binary, command.Process.Pid, time.Since(started).Round(time.Millisecond), ctx.Err() != nil)
	}()
	if runtime.GOOS == "linux" && !deviceMode {
		_ = unix.Setpriority(unix.PRIO_PROCESS, command.Process.Pid, 15)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(DrivingWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !deviceMode && (procKB(fmt.Sprintf("/proc/%d/status", command.Process.Pid), "VmRSS:") > MediaRSSLimitMiB*1024 || !memoryAvailable()) {
					_ = command.Process.Kill()
					return
				}
			}
		}
	}()
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return errors.New("processing stopped: canceled or vehicle no longer confirmed parked")
		}
		return errors.New(binary + " failed or exceeded memory budget: " + stderr.String())
	}
	return nil
}

type limitedBuffer struct{ value []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	length := len(p)
	if len(b.value) < 4096 {
		remaining := 4096 - len(b.value)
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.value = append(b.value, p...)
	}
	return length, nil
}
func (b *limitedBuffer) String() string { return string(b.value) }
func safePath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("absolute path rejected")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside recordings")
	}
	return path, nil
}

// A verification gate can only restrict work. It cannot override actual
// onroad/unknown firmware state and never writes to the firmware params.
func combinedState(actual, gate StateReader) StateReader {
	return func() string {
		real := actual()
		if real != "parked" {
			return real
		}
		return gate()
	}
}
