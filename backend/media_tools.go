package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Resolve relative to the running release, not a global install or current
// symlink. OTA therefore keeps an in-flight worker on its matching media build.
func mediaTool(name string) (string, error) {
	if name != "ffmpeg" && name != "ffprobe" {
		return "", fmt.Errorf("unsupported media tool %q", name)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(filepath.Dir(executable), "bin", name)
	if info, err := os.Lstat(candidate); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return "", fmt.Errorf("bundled %s is not a regular executable", name)
		}
		return candidate, nil
	}
	if deviceMode || os.Getenv("DASH_SUPERVISED") == "1" {
		return "", fmt.Errorf("bundled %s is missing; reinstall the complete Dash release", name)
	}
	return exec.LookPath(name)
}
