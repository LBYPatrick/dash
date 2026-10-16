package usb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"dash.local/backend/internal/archivepath"
	"golang.org/x/sys/unix"
)

type mountedVolume struct {
	volume Volume
	point  string
	child  *exec.Cmd
	done   chan struct{}
}
type helper struct {
	mu       sync.Mutex
	state    func() string
	uid, gid int
	mounts   map[string]*mountedVolume
	cached   []Volume
	probeKey string
	probedAt time.Time
}
type mountEntry struct{ root, point, filesystem, source, device string }

func mountEntries() ([]mountEntry, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	result := []mountEntry{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		split := -1
		for i, value := range fields {
			if value == "-" {
				split = i
				break
			}
		}
		if len(fields) < 6 || split < 0 || len(fields) <= split+2 {
			continue
		}
		result = append(result, mountEntry{fields[3], fields[4], fields[split+1], fields[split+2], fields[2]})
	}
	return result, scanner.Err()
}
func prepareMountBase() error {
	if err := safeRootDirectory("/data/dash/usb-tools", 0755); err != nil {
		return err
	}
	entries, err := mountEntries()
	if err != nil {
		return err
	}
	var existing, parent *mountEntry
	for i := range entries {
		entry := &entries[i]
		if entry.point == MountBase {
			existing = entry
		}
		if entry.point == "/data" {
			parent = entry
		}
	}
	if parent == nil {
		return errors.New("USB service requires the persistent data mount")
	}
	marker := "/data/dash/usb-tools/.base-owned"
	if existing != nil {
		data, err := os.ReadFile(marker)
		if err != nil || string(data) != "dash-usb-bind-v1\n" || existing.source != parent.source || existing.root != filepath.Join(parent.root, "dash/usb") {
			return errors.New("refusing a foreign USB mount root")
		}
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString("dash-usb-bind-v1\n"); err == nil {
		err = file.Sync()
	}
	file.Close()
	if err != nil {
		return err
	}
	if existing == nil {
		if err = unix.Mount(MountBase, MountBase, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
			return errors.New("cannot create the Dash USB mount root")
		}
	}
	if err = unix.Mount("", MountBase, "", unix.MS_SHARED, ""); err != nil {
		return errors.New("cannot enable Dash USB mount propagation")
	}
	return nil
}
func recordPath(uuid string) string {
	return filepath.Join(filepath.Dir(Socket), "mount-"+strings.ToLower(uuid)+".json")
}
func recordMount(volume Volume) error {
	data, err := json.Marshal(volume)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(recordPath(volume.UUID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	file.Close()
	return err
}
func (h *helper) recoverMounts() error {
	entries, err := os.ReadDir(filepath.Dir(Socket))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "mount-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("unsafe USB recovery record")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm() != 0600 {
			return errors.New("unsafe USB recovery record")
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(Socket), entry.Name()))
		if err != nil || len(data) > 8192 {
			return errors.New("invalid USB recovery record")
		}
		var volume Volume
		if json.Unmarshal(data, &volume) != nil || !uuidPattern.MatchString(volume.UUID) || entry.Name() != filepath.Base(recordPath(volume.UUID)) {
			return errors.New("invalid USB recovery record")
		}
		key := strings.ToLower(volume.UUID)
		h.mounts[key] = &mountedVolume{volume: volume, point: filepath.Join(MountBase, key)}
		if err = h.eject(key); err != nil {
			return err
		}
	}
	return nil
}

func safeRootDirectory(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("USB service directory is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return errors.New("USB service directory must be root-owned and not writable by other users")
	}
	return nil
}
func externalBlock(name string) (string, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9]+$`).MatchString(name) {
		return "", errors.New("invalid USB block device")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", name))
	if err != nil || !strings.Contains(resolved, "/usb") {
		return "", errors.New("only external USB storage is allowed")
	}
	// Refuse the internal data/root device and every parent/child partition of it,
	// even on an unusual installation whose system disk happens to use USB.
	for _, system := range []string{"/", "/data"} {
		var stat unix.Stat_t
		if unix.Stat(system, &stat) != nil {
			return "", errors.New("cannot identify protected storage")
		}
		systemPath, err := filepath.EvalSymlinks(fmt.Sprintf("/sys/dev/block/%d:%d", unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev))))
		if err != nil {
			return "", errors.New("cannot identify protected block storage")
		}
		if wholeBlock(systemPath) == wholeBlock(resolved) || systemPath == resolved || strings.HasPrefix(systemPath, resolved+"/") || strings.HasPrefix(resolved, systemPath+"/") {
			return "", errors.New("system storage is protected")
		}
	}
	return resolved, nil
}
func wholeBlock(resolved string) string {
	if _, err := os.Stat(filepath.Join(resolved, "partition")); err == nil {
		return filepath.Dir(resolved)
	}
	return resolved
}
func openExternal(name string, access int) (*os.File, error) {
	resolved, err := externalBlock(name)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/dev/"+name, access|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFBLK {
		file.Close()
		return nil, errors.New("USB device is not a block device")
	}
	raw, err := os.ReadFile(filepath.Join(resolved, "dev"))
	if err != nil || strings.TrimSpace(string(raw)) != fmt.Sprintf("%d:%d", unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev))) {
		file.Close()
		return nil, errors.New("USB device changed")
	}
	return file, nil
}
func probe(ctx context.Context, name string) (Volume, error) {
	var volume Volume
	file, err := openExternal(name, unix.O_RDONLY)
	if err != nil {
		return volume, err
	}
	defer file.Close()
	return probeFile(ctx, file, name)
}
func probeFile(ctx context.Context, file *os.File, name string) (Volume, error) {
	var volume Volume
	command := exec.CommandContext(ctx, "/sbin/blkid", "-p", "-o", "export", "/proc/self/fd/3")
	command.ExtraFiles = []*os.File{file}
	output, err := command.Output()
	if err != nil || len(output) > 8192 {
		return volume, errors.New("USB filesystem was not recognized")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = value
		}
	}
	if !uuidPattern.MatchString(values["UUID"]) {
		return volume, errors.New("USB filesystem needs a UUID")
	}
	if values["TYPE"] != "ext4" && values["TYPE"] != "exfat" && values["TYPE"] != "ntfs" {
		return volume, errors.New("supported USB filesystems are exFAT, ext4 and NTFS")
	}
	return Volume{UUID: values["UUID"], Label: values["LABEL"], Filesystem: values["TYPE"], Name: name, Available: true}, nil
}
func (h *helper) volumes(ctx context.Context) []Volume {
	if h.state() != "parked" {
		result := append([]Volume{}, h.cached...)
		for i := range result {
			result[i].Mounted = h.mounts[strings.ToLower(result[i].UUID)] != nil
		}
		return result
	}
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return []Volume{}
	}
	var signature strings.Builder
	for _, entry := range entries {
		if resolved, err := externalBlock(entry.Name()); err == nil {
			signature.WriteString(resolved)
			if info, err := os.Stat(filepath.Join("/sys/class/block", entry.Name())); err == nil {
				if stat, ok := info.Sys().(*syscall.Stat_t); ok {
					fmt.Fprintf(&signature, ":%d", stat.Ino)
				}
			}
			signature.WriteByte('\n')
		}
	}
	if signature.String() == h.probeKey && !h.probedAt.IsZero() && time.Since(h.probedAt) < 30*time.Second {
		result := append([]Volume{}, h.cached...)
		for i := range result {
			result[i].Mounted = h.mounts[strings.ToLower(result[i].UUID)] != nil
		}
		return result
	}
	result := []Volume{}
	counts := map[string]int{}
	for _, entry := range entries {
		if ctx.Err() != nil || h.state() != "parked" {
			break
		}
		if _, err := externalBlock(entry.Name()); err != nil {
			continue
		}
		limited, cancel := context.WithTimeout(ctx, 2*time.Second)
		volume, err := probe(limited, entry.Name())
		cancel()
		if err != nil {
			continue
		}
		counts[strings.ToLower(volume.UUID)]++
		result = append(result, volume)
	}
	for i := range result {
		key := strings.ToLower(result[i].UUID)
		result[i].Mounted = h.mounts[key] != nil
		result[i].Available = counts[key] == 1
	}
	h.cached = result
	h.probeKey, h.probedAt = signature.String(), time.Now()
	return append([]Volume{}, result...)
}
func (h *helper) mount(ctx context.Context, uuid string) error {
	if h.state() != "parked" {
		return errors.New("USB mounting requires confirmed parked state")
	}
	key := strings.ToLower(uuid)
	if current := h.mounts[key]; current != nil {
		if _, err := externalBlock(current.volume.Name); err != nil {
			return errors.New("USB drive disconnected")
		}
		var mountStat, parentStat unix.Stat_t
		if unix.Stat(current.point, &mountStat) != nil || unix.Stat(MountBase, &parentStat) != nil || mountStat.Dev == parentStat.Dev {
			return errors.New("USB mount disappeared")
		}
		return nil
	}
	var selected Volume
	for _, volume := range h.volumes(ctx) {
		if strings.EqualFold(volume.UUID, uuid) && volume.Available {
			selected = volume
		}
	}
	if selected.Name == "" {
		return errors.New("USB drive disconnected or its UUID is ambiguous")
	}
	point := filepath.Join(MountBase, key)
	if err := safeRootDirectory(point, 0755); err != nil {
		return err
	}
	var pointStat, baseStat unix.Stat_t
	if unix.Stat(point, &pointStat) != nil || unix.Stat(MountBase, &baseStat) != nil || pointStat.Dev != baseStat.Dev {
		return errors.New("USB mount point is already occupied")
	}
	if err := recordMount(selected); err != nil {
		return errors.New("USB recovery record could not be created")
	}
	mounted := mountedVolume{volume: selected, point: point}
	recorded := false
	defer func() {
		if !recorded {
			// Preserve the recovery record if a failed/canceled mount cannot
			// be cleaned. A mount may appear just as its startup is canceled.
			h.mounts[key] = &mounted
			_ = h.eject(key)
		}
	}()
	device, err := openExternal(selected.Name, unix.O_RDWR)
	if err != nil {
		return err
	}
	defer device.Close()
	entries, err := mountEntries()
	if err != nil {
		return errors.New("cannot validate existing USB mounts")
	}
	var deviceStat unix.Stat_t
	if err = unix.Fstat(int(device.Fd()), &deviceStat); err != nil {
		return errors.New("cannot validate USB device")
	}
	identityNumber := fmt.Sprintf("%d:%d", unix.Major(uint64(deviceStat.Rdev)), unix.Minor(uint64(deviceStat.Rdev)))
	for _, entry := range entries {
		if entry.device == identityNumber || entry.source == "/dev/"+selected.Name {
			return errors.New("USB filesystem is already mounted by another application")
		}
	}
	identity, err := probeFile(ctx, device, selected.Name)
	if err != nil || !strings.EqualFold(identity.UUID, selected.UUID) || identity.Filesystem != selected.Filesystem {
		return errors.New("USB device changed before mounting")
	}
	if selected.Filesystem == "ext4" {
		if err = unix.Mount(fmt.Sprintf("/proc/self/fd/%d", device.Fd()), point, "ext4", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_NOATIME, "errors=remount-ro"); err != nil {
			return errors.New("ext4 mount failed; check filesystem compatibility and health")
		}
	} else {
		tool := "mount.exfat-fuse"
		options := fmt.Sprintf("uid=%d,gid=%d,umask=0077,allow_other,default_permissions,nodev,nosuid,noexec,max_read=131072", h.uid, h.gid)
		if selected.Filesystem == "ntfs" {
			tool = "ntfs-3g"
			options += ",norecover,windows_names,no_detach"
		}
		binary := filepath.Join("/data/dash/usb-tools", tool)
		info, err := os.Lstat(binary)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return errors.New("bundled USB filesystem helper is unavailable; reinstall the complete release")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			return errors.New("USB filesystem helper must be root-owned")
		}
		// Both bundled implementations run a single-threaded request loop.
		// exFAT is patched to stay in the foreground; NTFS uses no_detach.
		command := exec.Command(binary, "-o", options, "/proc/self/fd/3", point)
		command.ExtraFiles = []*os.File{device}
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err = command.Start(); err != nil {
			return errors.New("USB filesystem helper could not start")
		}
		mounted.child = command
		mounted.done = make(chan struct{})
		go func() { _ = command.Wait(); close(mounted.done) }()
		ready := false
		for attempt := 0; attempt < 40; attempt++ {
			var a, b unix.Stat_t
			if unix.Stat(point, &a) == nil && unix.Stat(MountBase, &b) == nil && a.Dev != b.Dev {
				ready = true
				break
			}
			select {
			case <-mounted.done:
				return errors.New("USB filesystem helper refused this volume; no repair was attempted")
			case <-ctx.Done():
				_ = command.Process.Signal(syscall.SIGTERM)
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !ready {
			_ = command.Process.Signal(syscall.SIGTERM)
			return errors.New("USB filesystem mount timed out")
		}
	}
	h.mounts[key] = &mounted
	recorded = true
	if h.state() != "parked" {
		_ = h.eject(key)
		return errors.New("USB mounting stopped because parked state was lost")
	}
	return nil
}
func (h *helper) prepareDirectory(uuid, directory string) error {
	if h.state() != "parked" {
		return errors.New("USB directory setup requires confirmed parked state")
	}
	if err := archivepath.Validate(directory); err != nil {
		return err
	}
	key := strings.ToLower(uuid)
	mounted := h.mounts[key]
	if mounted == nil {
		return errors.New("USB drive is not mounted")
	}
	fd, err := unix.Open(mounted.point, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = h.eject(key)
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	parts := []string{}
	if directory != "" {
		parts = strings.Split(directory, "/")
	}
	parts = append(parts, "dash-archive")
	for i, part := range parts {
		if h.state() != "parked" {
			return errors.New("USB directory setup stopped because parked state was lost")
		}
		createErr := unix.Mkdirat(fd, part, 0700)
		if createErr != nil && !errors.Is(createErr, unix.EEXIST) {
			return errors.New("USB save folder could not be created")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errors.New("USB save folder is unavailable or contains a link")
		}
		if createErr == nil {
			if err = unix.Fchown(next, h.uid, h.gid); err != nil {
				unix.Close(next)
				return errors.New("new USB save folder permissions could not be set")
			}
			if err = unix.Fsync(next); err == nil {
				err = unix.Fsync(fd)
			}
			if err != nil {
				unix.Close(next)
				return errors.New("new USB save folder could not be synchronized")
			}
		}
		unix.Close(fd)
		fd = next
		if i == len(parts)-1 {
			var stat unix.Stat_t
			if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(h.uid) {
				return errors.New("existing archive directory is not owned by comma")
			}
		}
	}
	return nil
}
func (h *helper) eject(key string) error {
	mounted := h.mounts[strings.ToLower(key)]
	if mounted == nil {
		return nil
	}
	if err := unix.Unmount(mounted.point, 0); err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
		return errors.New("USB drive is still busy; pause the transfer before ejecting")
	}
	if mounted.child != nil {
		_ = mounted.child.Process.Signal(syscall.SIGTERM)
		select {
		case <-mounted.done:
		case <-time.After(time.Second):
			_ = mounted.child.Process.Kill()
			select {
			case <-mounted.done:
			case <-time.After(time.Second):
				return errors.New("USB helper process has not stopped")
			}
		}
	}
	delete(h.mounts, strings.ToLower(key))
	_ = os.Remove(recordPath(key))
	return nil
}
func (h *helper) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var input struct {
		UUID      string `json:"uuid"`
		Directory string `json:"directory"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&input); err != nil {
		w.WriteHeader(400)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var result any = struct{}{}
	var err error
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if r.URL.Path == "/list" || r.URL.Path == "/mount" {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if h.state() != "parked" {
						cancel()
						return
					}
				}
			}
		}()
	}
	switch r.URL.Path {
	case "/list":
		result = h.volumes(ctx)
	case "/mount":
		if !uuidPattern.MatchString(input.UUID) {
			err = errors.New("invalid USB volume UUID")
		} else if err = archivepath.Validate(input.Directory); err != nil {
			// Reject invalid paths before touching the volume.
		} else {
			err = h.mount(ctx, input.UUID)
			if err == nil {
				err = h.prepareDirectory(input.UUID, input.Directory)
			}
			result = map[string]bool{"mounted": err == nil}
		}
	case "/eject":
		if !uuidPattern.MatchString(input.UUID) {
			err = errors.New("invalid USB volume UUID")
		} else {
			err = h.eject(input.UUID)
		}
	default:
		w.WriteHeader(404)
		return
	}
	if err != nil {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
func Serve(ctx context.Context, state func() string) error {
	if os.Geteuid() != 0 {
		return errors.New("USB helper requires its root-owned systemd service")
	}
	comma, err := user.Lookup("comma")
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(comma.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(comma.Gid)
	if err != nil {
		return err
	}
	if err = safeRootDirectory(MountBase, 0755); err != nil {
		return err
	}
	if err = safeRootDirectory(filepath.Dir(Socket), 0750); err != nil {
		return err
	}
	if err = prepareMountBase(); err != nil {
		return err
	}
	if err = os.Chown(filepath.Dir(Socket), 0, gid); err != nil {
		return err
	}
	if info, err := os.Lstat(Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("USB socket path is occupied")
		}
		if err = os.Remove(Socket); err != nil {
			return err
		}
	}
	listener, err := net.Listen("unix", Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(Socket)
	if err = os.Chmod(Socket, 0660); err != nil {
		return err
	}
	if err = os.Chown(Socket, 0, gid); err != nil {
		return err
	}
	h := &helper{state: state, uid: uid, gid: gid, mounts: map[string]*mountedVolume{}, cached: []Volume{}}
	if err = h.recoverMounts(); err != nil {
		return err
	}
	server := &http.Server{Handler: http.HandlerFunc(h.handler), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	shutdown := make(chan struct{})
	go func() {
		defer close(shutdown)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = server.Close()
				h.mu.Lock()
				for key := range h.mounts {
					if child := h.mounts[key].child; child != nil {
						_ = child.Process.Signal(syscall.SIGTERM)
					}
					_ = h.eject(key)
				}
				h.mu.Unlock()
				_ = unix.Unmount(MountBase, 0)
				return
			case <-ticker.C:
				if state() != "parked" {
					h.mu.Lock()
					for key := range h.mounts {
						// Stop userspace filesystem work even if an in-flight
						// backend descriptor temporarily makes unmount busy.
						if child := h.mounts[key].child; child != nil {
							_ = child.Process.Signal(syscall.SIGTERM)
						}
						_ = h.eject(key)
					}
					h.mu.Unlock()
				}
			}
		}
	}()
	err = server.Serve(listener)
	if ctx.Err() != nil {
		<-shutdown
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
