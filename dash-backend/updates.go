package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type UpdateConfig struct {
	Repository string `json:"repository"`
	Channel    string `json:"channel"`
	Automatic  bool   `json:"automatic"`
}
type UpdateState struct {
	Repository    string           `json:"repository"`
	PendingHealth bool             `json:"pendingHealth"`
	Managed       bool             `json:"managed"`
	Phase         string           `json:"phase"`
	Current       Build            `json:"current"`
	Channel       string           `json:"channel"`
	Automatic     bool             `json:"automatic"`
	Release       *ReleaseManifest `json:"release,omitempty"`
	Transferred   int64            `json:"transferred,omitempty"`
	Total         int64            `json:"total,omitempty"`
	Message       string           `json:"message,omitempty"`
}
type UpdateManager struct {
	mu      sync.Mutex
	home    string
	config  UpdateConfig
	status  UpdateState
	state   StateReader
	ctx     context.Context
	client  *http.Client
	busy    bool
	restart func()
}

func newUpdater(ctx context.Context, home string, state StateReader, restart func()) (*UpdateManager, error) {
	return newUpdaterForBuild(ctx, home, state, restart, currentBuild())
}

func newUpdaterForBuild(ctx context.Context, home string, state StateReader, restart func(), build Build) (*UpdateManager, error) {
	manager := &UpdateManager{ctx: ctx, home: home, state: state, client: releaseClient(), restart: restart, config: UpdateConfig{"LBYPatrick/dash", "main", true}}
	manager.status = UpdateState{Current: build, Phase: "unmanaged", Channel: "main"}
	if home == "" || build.Channel == "local" {
		return manager, nil
	}
	if data, err := readSmallFile(filepath.Join(home, "update.json"), 64<<10); err == nil {
		if len(data) > 16<<10 || json.Unmarshal(data, &manager.config) != nil {
			return nil, errors.New("invalid update configuration")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if !validChannel(manager.config.Channel) || !repositoryPattern.MatchString(manager.config.Repository) {
		return nil, errors.New("invalid update channel or repository")
	}
	manager.status.Repository = manager.config.Repository
	manager.status.Managed = true
	manager.status.Phase = "idle"
	manager.status.Channel = manager.config.Channel
	manager.status.Automatic = manager.config.Automatic
	// A completed download survives browser dismissal and service/device restarts.
	var ready ReleaseManifest
	if data, err := readSmallFile(filepath.Join(home, "ready.json"), 64<<10); err == nil && len(data) <= 64<<10 && json.Unmarshal(data, &ready) == nil && ready.Channel == manager.config.Channel && ready.Tag != manager.status.Current.Tag {
		if _, err := ready.asset(manager.config.Channel, runtime.GOARCH); err == nil {
			expected := ready.Build
			expected.Arch = runtime.GOARCH
			if build, err := readBuild(filepath.Join(home, "releases", ready.Tag)); err == nil && build == expected {
				manager.status.Release = &ready
				manager.status.Phase = "ready"
			}
		}
	}
	var failure struct {
		Tag     string `json:"tag"`
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if data, err := readSmallFile(filepath.Join(home, "update-failure.json"), 64<<10); err == nil && len(data) < 16<<10 && json.Unmarshal(data, &failure) == nil {
		manager.status.Message = failure.Message
		if failure.Kind != "postponed" && manager.status.Release != nil && manager.status.Release.Tag == failure.Tag {
			manager.status.Phase = "error"
			manager.status.Release = nil
		}
	}
	return manager, nil
}
func (u *UpdateManager) snapshot() UpdateState {
	u.mu.Lock()
	defer u.mu.Unlock()
	status := u.status
	if status.Managed {
		if _, err := os.Stat(filepath.Join(u.home, "activation.json")); err == nil {
			status.PendingHealth = true
			status.Phase = "restarting"
			status.Message = "Checking the new build before completing installation."
		}
	}
	return status
}
func (u *UpdateManager) set(phase, message string) {
	u.mu.Lock()
	u.status.Phase = phase
	u.status.Message = message
	u.mu.Unlock()
	log.Printf("update phase=%s: %s", phase, message)
}
func (u *UpdateManager) configure(channel string, automatic bool) error {
	if !validChannel(channel) {
		return errors.New("choose main, nightly, or dev")
	}
	if u.state() != "parked" {
		return errors.New("update preferences require confirmed parked state")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.status.Managed {
		return errors.New("this development install is not OTA managed")
	}
	if u.busy {
		return errors.New("an update operation is running")
	}
	config := u.config
	config.Channel = channel
	config.Automatic = automatic
	if err := atomicJSON(filepath.Join(u.home, "update.json"), config); err != nil {
		return err
	}
	if channel != u.config.Channel {
		u.status.Release = nil
		u.status.Phase = "idle"
		u.status.Message = ""
	}
	u.config = config
	u.status.Channel = channel
	u.status.Automatic = automatic
	return nil
}
func (u *UpdateManager) begin(action string) error {
	if u.state() != "parked" {
		return errors.New("updates require confirmed parked state")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.status.Managed {
		return errors.New("install a CI release to enable OTA updates")
	}
	if u.busy {
		return errors.New("an update operation is running")
	}
	if action == "check" && u.status.Phase == "ready" {
		return nil
	}
	if action == "download" && (u.status.Release == nil || u.status.Phase != "available") {
		return errors.New("check for an available release first")
	}
	if action != "check" && action != "download" {
		return errors.New("invalid update action")
	}
	u.busy = true
	u.status.Message = ""
	u.status.Transferred = 0
	u.status.Total = 0
	if action == "check" {
		u.status.Phase = "checking"
	} else {
		u.status.Phase = "downloading"
	}
	config := u.config
	release := u.status.Release
	go func() {
		defer func() { u.mu.Lock(); u.busy = false; u.mu.Unlock() }()
		ctx, cancel := parkedContext(u.ctx, u.state)
		defer cancel()
		ctx, timeout := context.WithTimeout(ctx, 15*time.Minute)
		defer timeout()
		unlock, err := acquireCompute(ctx)
		if err == nil {
			defer unlock()
			if action == "check" {
				err = u.check(ctx, config)
			} else {
				err = u.download(ctx, config, *release)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				u.set("error", "Update stopped: canceled or vehicle no longer confirmed parked. Check again while parked.")
			} else {
				u.set("error", err.Error())
			}
		}
	}()
	return nil
}
func (u *UpdateManager) check(ctx context.Context, config UpdateConfig) error {
	manifest, err := readManifest(ctx, u.client, config.Repository, config.Channel, "")
	if err != nil {
		return err
	}
	if _, err := manifest.asset(config.Channel, runtime.GOARCH); err != nil {
		return err
	}
	var failure struct {
		Tag  string `json:"tag"`
		Kind string `json:"kind"`
	}
	if data, err := readSmallFile(filepath.Join(u.home, "update-failure.json"), 16<<10); err == nil && json.Unmarshal(data, &failure) == nil && failure.Kind != "postponed" && failure.Tag == manifest.Tag {
		return errors.New("This build failed its startup check. Choose another channel or check again after a newer build is published.")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if manifest.Tag == u.status.Current.Tag || (u.status.Current.Channel == config.Channel && manifest.Sequence <= u.status.Current.Sequence) {
		u.status.Release = nil
		u.status.Phase = "current"
		return nil
	}
	u.status.Release = &manifest
	u.status.Phase = "available"
	return nil
}
func (u *UpdateManager) download(ctx context.Context, config UpdateConfig, manifest ReleaseManifest) error {
	releases := filepath.Join(u.home, "releases")
	if err := os.MkdirAll(releases, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(releases)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			if err := removeUpdateTree(ctx, filepath.Join(releases, entry.Name())); err != nil {
				return err
			}
		}
	}
	keep := map[string]bool{manifest.Tag: true, u.status.Current.Tag: true}
	for _, link := range []string{"current", "previous"} {
		if tag, err := releaseTarget(u.home, link); err == nil {
			keep[tag] = true
		}
	}
	for _, entry := range entries {
		if keep[entry.Name()] || !entry.IsDir() || !releaseIDPattern.MatchString(entry.Name()) {
			continue
		}
		if _, err := readBuild(filepath.Join(releases, entry.Name())); err != nil {
			continue
		}
		if err := removeUpdateTree(ctx, filepath.Join(releases, entry.Name())); err != nil {
			return err
		}
	}
	var disk unix.Statfs_t
	if err := unix.Statfs(u.home, &disk); err != nil {
		return err
	}
	if uint64(disk.Bavail)*uint64(disk.Bsize) < 2<<30 {
		return errors.New("updates require 2 GiB free space; recordings will not be removed")
	}
	stage, err := os.MkdirTemp(releases, ".staging-")
	if err != nil {
		return err
	}
	defer func() {
		if u.state() == "parked" {
			_ = removeUpdateTree(ctx, stage)
		}
	}()
	archive := filepath.Join(stage, "download.tar.gz")
	err = downloadRelease(ctx, u.client, config.Repository, manifest, runtime.GOARCH, archive, func(read, total int64) {
		u.mu.Lock()
		u.status.Transferred = read
		u.status.Total = total
		u.mu.Unlock()
	})
	if err != nil {
		return err
	}
	u.set("verifying", "Verifying downloaded build")
	payload := filepath.Join(stage, "payload")
	if err := os.Mkdir(payload, 0700); err != nil {
		return err
	}
	expected := manifest.Build
	expected.Arch = runtime.GOARCH
	if err := unpackRelease(ctx, archive, payload, expected); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := filepath.Join(releases, manifest.Tag)
	if _, err := os.Lstat(destination); err == nil {
		build, err := readBuild(destination)
		if err != nil || build != expected {
			return errors.New("existing release directory has different content; reinstall over SSH")
		}
		if manifest.Tag == u.status.Current.Tag {
			return errors.New("cannot replace the running release")
		}
		// Use the freshly verified bytes rather than trusting an old directory's
		// metadata. Renames are bounded; parked-only cleanup handles the old copy.
		retired := filepath.Join(stage, "retired")
		if err := os.Rename(destination, retired); err != nil {
			return err
		}
		if err := os.Rename(payload, destination); err != nil {
			_ = os.Rename(retired, destination)
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	} else if err := os.Rename(payload, destination); err != nil {
		return err
	}
	if err := syncDirectory(releases); err != nil {
		return err
	}
	if err := atomicJSON(filepath.Join(u.home, "ready.json"), manifest); err != nil {
		return err
	}
	u.set("ready", "Download verified. Restart Dash to install while parked.")
	return nil
}
func (u *UpdateManager) install() error {
	if u.state() != "parked" {
		return errors.New("installation requires confirmed parked state")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.status.Managed || u.busy || u.status.Phase != "ready" || u.status.Release == nil || u.restart == nil {
		return errors.New("no verified update is ready")
	}
	release := u.status.Release
	build, err := readBuild(filepath.Join(u.home, "releases", release.Tag))
	if err != nil {
		return err
	}
	expected := release.Build
	expected.Arch = runtime.GOARCH
	if build != expected || build.Channel != u.config.Channel {
		return errors.New("ready update identity changed")
	}
	if err := atomicJSON(filepath.Join(u.home, "pending.json"), map[string]string{"tag": release.Tag}); err != nil {
		return err
	}
	u.status.Phase = "restarting"
	u.status.Message = "Restarting Dash; this page will reconnect."
	// Let the accepted HTTP response reach the browser before draining the server.
	go func() {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-u.ctx.Done():
		case <-timer.C:
			u.restart()
		}
	}()
	return nil
}
func (u *UpdateManager) automaticChecks() {
	if !u.snapshot().Managed {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	next := time.Now().Add(time.Minute)
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			status := u.snapshot()
			if !status.Automatic || u.state() != "parked" || time.Now().Before(next) || status.Phase == "ready" || status.Phase == "available" {
				continue
			}
			if err := u.begin("check"); err == nil {
				next = time.Now().Add(6 * time.Hour)
			}
		}
	}
}
func (s *Server) updateAPI(w http.ResponseWriter, r *http.Request) {
	if s.updates == nil && r.Method != "GET" {
		fail(w, 409, errors.New("this source build is not OTA managed"))
		return
	}
	if s.updates == nil {
		writeJSON(w, 200, UpdateState{Phase: "unmanaged", Current: currentBuild(), Channel: "main"})
		return
	}
	if r.Method == "GET" {
		writeJSON(w, 200, s.updates.snapshot())
		return
	}
	var input struct {
		Action    string `json:"action"`
		Channel   string `json:"channel"`
		Automatic bool   `json:"automatic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, 400, err)
		return
	}
	var err error
	switch input.Action {
	case "configure":
		err = s.updates.configure(input.Channel, input.Automatic)
	case "check", "download":
		err = s.updates.begin(input.Action)
	case "install":
		err = s.updates.install()
	default:
		err = fmt.Errorf("unknown update action")
	}
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 202, s.updates.snapshot())
}

func removeUpdateTree(ctx context.Context, root string) error {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		// A stage can contain the new payload and a retired copy being repaired.
		if len(paths) > 8192 {
			return errors.New("update cleanup exceeds file budget")
		}
		paths = append(paths, path)
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for n := len(paths) - 1; n >= 0; n-- {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := os.Remove(paths[n]); err != nil {
			return err
		}
	}
	return nil
}
