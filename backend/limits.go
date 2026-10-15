package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Verify the kernel's actual limits before starting any worker. Unit text alone
// is insufficient: unsupported controllers may otherwise be silently ignored.
func verifyLimits(directory string) error { return verifyKernelLimits(directory, true) }

func verifyKernelLimits(directory string, requireHigh bool) error {
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(directory, name))
		return strings.TrimSpace(string(data)), err
	}
	cpu, err := read("cpu.max")
	if err != nil {
		return errors.New("CPU controller unavailable; Dash requires cgroup v2 with cpu, memory and pids controllers")
	}
	fields := strings.Fields(cpu)
	if len(fields) != 2 {
		return errors.New("invalid cpu.max")
	}
	quota, e1 := strconv.ParseInt(fields[0], 10, 64)
	period, e2 := strconv.ParseInt(fields[1], 10, 64)
	if e1 != nil || e2 != nil || quota <= 0 || period <= 0 || quota > period/2 {
		return fmt.Errorf("CPU quota is not bounded to 50%% of one core: %s", cpu)
	}
	limits := map[string]int64{"memory.max": 384 << 20, "memory.swap.max": 0, "pids.max": 64}
	if requireHigh {
		limits["memory.high"] = 256 << 20
	}
	for name, limit := range limits {
		text, err := read(name)
		if err != nil {
			return fmt.Errorf("required control %s unavailable: %w", name, err)
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || value < 0 || value > limit || (limit > 0 && value == 0) {
			return fmt.Errorf("%s is not enforced within %d (actual %s)", name, limit, text)
		}
	}
	return nil
}
func verifySystemdLimits() error {
	if os.Getenv("INVOCATION_ID") == "" {
		return errors.New("run the installed dash.service through systemd")
	}
	directory, err := currentCgroup()
	if err != nil {
		return err
	}
	if filepath.Base(directory) != "dash.service" {
		return errors.New("expected dash.service cgroup")
	}
	return verifyLimits(directory)
}

func currentCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
			// Docker's private cgroup namespace exposes the current group as '/'.
			candidate := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(fields[2], "/"))
			if _, err := os.Stat(filepath.Join(candidate, "cpu.max")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", errors.New("unified cgroup v2 controls unavailable")
}
