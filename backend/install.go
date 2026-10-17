package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Keep both installation routes on the same reviewed lifecycle scripts.
//
//go:embed preflight-install.sh activate-install.sh install-service.sh systemd-registration.sh uninstall-service.sh boot-state.sh verify-service.sh systemd/dash.service systemd/dash-usb.service
var installFiles embed.FS

func runInstallScript(name string, args ...string) error {
	data, err := installFiles.ReadFile(name)
	if err != nil {
		return err
	}
	command := exec.Command("bash", append([]string{"-s", "--"}, args...)...)
	command.Stdin = strings.NewReader(string(data))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

// installDeviceRelease is an explicit CLI operation, never a server background task.
func installDeviceRelease(repository, channel, tag string, temporary bool) error {
	if runtime.GOOS != "linux" || !repositoryPattern.MatchString(repository) || !validChannel(channel) || (tag != "" && !releaseIDPattern.MatchString(tag)) {
		return errors.New("installation requires Linux and a valid repository/channel/tag")
	}
	if os.Getuid() == 0 {
		return errors.New("run the installer as comma, without sudo; it elevates only service registration")
	}
	runtime.GOMAXPROCS(IdleGoProcs)
	debug.SetMemoryLimit(GoHeapTargetMiB << 20)
	mode := "false"
	if temporary {
		mode = "true"
	}
	if err := runInstallScript("preflight-install.sh", mode); err != nil {
		return fmt.Errorf("installation preflight: %w", err)
	}
	state := paramsState("/data/params/d")
	if state() != "parked" {
		return errors.New("installation requires confirmed parked state")
	}
	home := "/data/dash"
	command := exec.Command("sudo", "-n", "install", "-d", "-o", "root", "-g", "comma", "-m", "1770", home)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(home, ".install.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("another Dash installation is running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err := requireRecordingSpace(home, 0); err != nil {
		return fmt.Errorf("insufficient space to stage Dash safely above the recording reserve; free space without deleting original recordings: %w", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Abort network and decompression work as soon as firmware leaves parked state.
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if state() != "parked" {
					cancel()
					return
				}
			}
		}
	}()
	stage, err := os.MkdirTemp(home, ".one-line-")
	if err != nil {
		return err
	}
	defer func() {
		if state() == "parked" {
			_ = os.RemoveAll(stage)
		}
	}()
	client := releaseClient()
	fmt.Printf("Downloading Dash %s release metadata…\n", channel)
	manifest, err := readManifest(ctx, client, repository, channel, tag)
	if err != nil {
		return err
	}
	if _, err = manifest.asset(channel, runtime.GOARCH); err != nil {
		return err
	}
	archive := filepath.Join(stage, "download.tar.gz")
	fmt.Printf("Downloading and verifying %s…\n", manifest.Tag)
	if err = downloadRelease(ctx, client, repository, manifest, runtime.GOARCH, archive, nil); err != nil {
		return err
	}
	payload := filepath.Join(stage, "payload")
	if err = os.Mkdir(payload, 0700); err != nil {
		return err
	}
	expected := manifest.Build
	expected.Arch = runtime.GOARCH
	if err = unpackRelease(ctx, archive, payload, expected); err != nil {
		return err
	}
	if err = verifyMediaBundle(payload, runtime.GOARCH); err != nil {
		return err
	}
	if ctx.Err() != nil || state() != "parked" {
		return errors.New("installation stopped: device is no longer confirmed parked")
	}
	if err = os.MkdirAll(filepath.Join(home, "releases"), 0700); err != nil {
		return err
	}
	if err = os.RemoveAll(filepath.Join(home, ".install.new")); err != nil {
		return err
	}
	if err = os.Rename(payload, filepath.Join(home, ".install.new")); err != nil {
		return err
	}
	for source, destination := range map[string]string{
		"install-service.sh": ".install-service.new", "systemd-registration.sh": ".registration.new",
		"uninstall-service.sh": ".uninstall-service.new", "boot-state.sh": ".boot-state.new",
		"verify-service.sh": ".verify-service.new", "systemd/dash.service": ".service.new",
		"systemd/dash-usb.service": ".usb-service.new",
	} {
		data, err := installFiles.ReadFile(source)
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(home, destination), data, 0600); err != nil {
			return err
		}
	}
	startup := "persistent"
	if temporary {
		startup = "runtime"
	}
	if err = os.WriteFile(filepath.Join(home, ".startup.new"), []byte(startup), 0600); err != nil {
		return err
	}
	if err = atomicJSON(filepath.Join(home, ".update.new"), UpdateConfig{repository, channel, true}); err != nil {
		return err
	}
	fmt.Println("Activating the verified release and checking systemd startup…")
	if err = runInstallScript("activate-install.sh"); err != nil {
		return fmt.Errorf("activation: %w", err)
	}
	fmt.Println("Installed. Open http://DEVICE_IP:8088 on the same network.")
	return nil
}
