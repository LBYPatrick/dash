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
	"strings"
	"time"
)

type StateReader func() string

func paramsState(directory string) StateReader {
	return func() string {
		on, err1 := os.ReadFile(filepath.Join(directory, "IsOnroad"))
		off, err2 := os.ReadFile(filepath.Join(directory, "IsOffroad"))
		if err1 == nil && strings.TrimSpace(string(on)) == "1" {
			return "onroad"
		}
		if err1 == nil && err2 == nil && strings.TrimSpace(string(on)) == "0" && strings.TrimSpace(string(off)) == "1" {
			return "parked"
		}
		return "unknown"
	}
}
func parkedContext(parent context.Context, state StateReader) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	if state() != "parked" {
		cancel()
		return ctx, cancel
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
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
	return runMediaCommand(ctx, "ffmpeg", nil, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-max_alloc", "33554432", "-threads", "1", "-filter_threads", "1"}, args...)...)
}
func runMediaCommand(ctx context.Context, binary string, stdout io.Writer, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, args...)
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
	if runtime.GOOS == "linux" {
		_ = unix.Setpriority(unix.PRIO_PROCESS, command.Process.Pid, 15)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if procKB(fmt.Sprintf("/proc/%d/status", command.Process.Pid), "VmRSS:") > 192*1024 || !memoryAvailable() {
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
