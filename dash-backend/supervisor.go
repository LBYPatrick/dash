package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

type activation struct {
	Previous  string `json:"previous"`
	Candidate string `json:"candidate"`
}

func releaseTarget(home, name string) (string, error) {
	target, err := os.Readlink(filepath.Join(home, name))
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) || filepath.Dir(target) != "releases" || !releaseIDPattern.MatchString(filepath.Base(target)) {
		return "", errors.New("invalid installed release link")
	}
	return filepath.Base(target), nil
}
func switchRelease(home, name, tag string) error {
	if !releaseIDPattern.MatchString(tag) {
		return errors.New("invalid release tag")
	}
	temporary := filepath.Join(home, "."+name+"-next")
	_ = os.Remove(temporary)
	if err := os.Symlink(filepath.Join("releases", tag), temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(home, name)); err != nil {
		return err
	}
	return syncDirectory(home)
}
func beginActivation(home, candidate string) error {
	previous, err := releaseTarget(home, "current")
	if err != nil {
		return err
	}
	if candidate == previous {
		return errors.New("release is already active")
	}
	if _, err := readBuild(filepath.Join(home, "releases", candidate)); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(home, "activation.json"), activation{previous, candidate}); err != nil {
		return err
	}
	return switchRelease(home, "current", candidate)
}
func recoverActivation(home string) error {
	data, err := readSmallFile(filepath.Join(home, "activation.json"), 64<<10)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var transaction activation
	if len(data) > 16<<10 || json.Unmarshal(data, &transaction) != nil || !releaseIDPattern.MatchString(transaction.Previous) || !releaseIDPattern.MatchString(transaction.Candidate) {
		return errors.New("invalid activation journal; recover over SSH")
	}
	if _, err := readBuild(filepath.Join(home, "releases", transaction.Previous)); err != nil {
		return fmt.Errorf("previous release unavailable: %w", err)
	}
	if err := switchRelease(home, "current", transaction.Previous); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(home, "update-failure.json"), map[string]string{"tag": transaction.Candidate, "kind": "startup", "message": "The new build did not complete its startup health check. Dash restored " + transaction.Previous + "."}); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(home, "pending.json"))
	if err := os.Remove(filepath.Join(home, "activation.json")); err != nil {
		return err
	}
	return syncDirectory(home)
}
func commitActivation(home string) error {
	data, err := readSmallFile(filepath.Join(home, "activation.json"), 64<<10)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var transaction activation
	if json.Unmarshal(data, &transaction) != nil {
		return errors.New("invalid activation journal")
	}
	if err := switchRelease(home, "previous", transaction.Previous); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(home, "activation.json")); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(home, "ready.json"))
	_ = os.Remove(filepath.Join(home, "pending.json"))
	_ = os.Remove(filepath.Join(home, "update-failure.json"))
	return syncDirectory(home)
}
func pendingRelease(home string) (string, error) {
	data, err := readSmallFile(filepath.Join(home, "pending.json"), 64<<10)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var pending struct {
		Tag string `json:"tag"`
	}
	if len(data) > 16<<10 || json.Unmarshal(data, &pending) != nil || !releaseIDPattern.MatchString(pending.Tag) {
		return "", errors.New("invalid pending release")
	}
	// The server writes ready.json only after verifying the immutable archive.
	var ready ReleaseManifest
	data, err = readSmallFile(filepath.Join(home, "ready.json"), 64<<10)
	if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &ready) != nil || ready.Tag != pending.Tag {
		return "", errors.New("pending update has no verified download")
	}
	if _, err := ready.asset(ready.Channel, runtime.GOARCH); err != nil {
		return "", err
	}
	actual, err := readBuild(filepath.Join(home, "releases", pending.Tag))
	expected := ready.Build
	expected.Arch = runtime.GOARCH
	if err != nil || actual != expected {
		return "", errors.New("pending release identity mismatch")
	}
	return pending.Tag, nil
}

// One stable bootstrap supervises the replaceable backend in the same systemd
// cgroup. It never invokes a shell, sudo, package manager or firmware manager.
// A bootstrap/protocol or unit upgrade requires SSH; app updates need no root.
func runSupervisor(ctx context.Context, home string, state StateReader, args []string) error {
	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(24 << 20)
	debug.SetGCPercent(60)
	serviceReady := filepath.Join(home, "cache", "supervisor-ready.json")
	_ = os.Remove(serviceReady)
	defer os.Remove(serviceReady)
	if err := recoverActivation(home); err != nil {
		return err
	}
	for {
		_ = os.Remove(serviceReady)
		tag, err := releaseTarget(home, "current")
		if err != nil {
			return err
		}
		folder := filepath.Join(home, "releases", tag)
		build, err := readBuild(folder)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(folder); err != nil || !info.IsDir() {
			return errors.New("release directory must not be a symlink")
		}
		ready, err := os.CreateTemp(filepath.Join(home, "cache"), ".ready-")
		if err != nil {
			return err
		}
		readyPath := ready.Name()
		ready.Close()
		_ = os.Remove(readyPath)
		childArgs := append(append([]string{}, args...), "--app", filepath.Join(folder, "dist"), "--update-home", home, "--ready-file", readyPath)
		child := exec.CommandContext(ctx, filepath.Join(folder, "dash-backend"), childArgs...)
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		child.Env = append(os.Environ(), "DASH_SUPERVISED=1")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		child.Cancel = func() error { return syscall.Kill(-child.Process.Pid, syscall.SIGTERM) }
		child.WaitDelay = 5 * time.Second
		if err := child.Start(); err != nil {
			_ = os.Remove(readyPath)
			if _, journalErr := os.Stat(filepath.Join(home, "activation.json")); journalErr == nil {
				if err := recoverActivation(home); err != nil {
					return err
				}
				continue
			}
			return err
		}
		log.Printf("bootstrap child pid=%d release=%s", child.Process.Pid, tag)
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		probation := true
		readyAt := time.Time{}
		deadline := time.NewTimer(45 * time.Second)
		tick := time.NewTicker(100 * time.Millisecond)
		var childErr error
	monitor:
		for {
			select {
			case childErr = <-done:
				break monitor
			case <-ctx.Done():
				childErr = <-done
				break monitor
			case <-deadline.C:
				if probation {
					_ = child.Process.Signal(syscall.SIGTERM)
					select {
					case childErr = <-done:
					case <-time.After(5 * time.Second):
						_ = child.Process.Kill()
						childErr = <-done
					}
					childErr = errors.New("backend startup health deadline exceeded")
					break monitor
				}
			case <-tick.C:
				if !probation {
					continue
				}
				data, err := readSmallFile(readyPath, 1024)
				var marker struct {
					PID      int    `json:"pid"`
					Revision string `json:"revision"`
					Tag      string `json:"tag"`
				}
				healthy := err == nil && len(data) < 1024 && json.Unmarshal(data, &marker) == nil && marker.PID == child.Process.Pid && marker.Revision == build.Revision && marker.Tag == build.Tag
				if !healthy {
					continue
				}
				if readyAt.IsZero() {
					readyAt = time.Now()
				}
				if time.Since(readyAt) >= 10*time.Second {
					healthErr := commitActivation(home)
					if healthErr == nil {
						healthErr = atomicJSON(serviceReady, map[string]any{"bootstrap": os.Getpid(), "child": child.Process.Pid, "tag": build.Tag})
					}
					if healthErr != nil {
						_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
						<-done
						tick.Stop()
						deadline.Stop()
						_ = os.Remove(readyPath)
						return healthErr
					}
					probation = false
					tick.Stop()
					deadline.Stop()
					log.Printf("bootstrap healthy release=%s", tag)
				}
			}
		}
		tick.Stop()
		deadline.Stop()
		_ = os.Remove(serviceReady)
		_ = syscall.Kill(-child.Process.Pid, syscall.SIGKILL)
		_ = os.Remove(readyPath)
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("bootstrap child exited release=%s error=%v", tag, childErr)
		if _, err := os.Stat(filepath.Join(home, "activation.json")); err == nil {
			if err := recoverActivation(home); err != nil {
				return err
			}
			continue
		}
		candidate, err := pendingRelease(home)
		if err != nil {
			return err
		}
		if candidate != "" {
			if state() != "parked" {
				_ = os.Remove(filepath.Join(home, "pending.json"))
				_ = atomicJSON(filepath.Join(home, "update-failure.json"), map[string]string{"tag": candidate, "kind": "postponed", "message": "Installation was postponed because parked state changed. Retry while parked."})
				continue
			}
			if err := beginActivation(home, candidate); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("backend exited without an update request: %v", childErr)
	}
}
func supervisorArgs(args []string) []string {
	result := []string{}
	for n := 0; n < len(args); n++ {
		argument := args[n]
		if argument == "--supervise" || argument == "-supervise" {
			continue
		}
		if argument == "--app" || argument == "-app" || argument == "--ready-file" || argument == "--update-home" {
			n++
			continue
		}
		if stringsHasFlagValue(argument, "supervise") || stringsHasFlagValue(argument, "app") || stringsHasFlagValue(argument, "ready-file") || stringsHasFlagValue(argument, "update-home") {
			continue
		}
		result = append(result, argument)
	}
	return result
}
func stringsHasFlagValue(value, name string) bool {
	return len(value) > len(name)+2 && (strings.HasPrefix(value, "--"+name+"=") || strings.HasPrefix(value, "-"+name+"="))
}
