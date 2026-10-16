package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Opt-in, isolated Linux mount namespace only. Never invoked by the device or
// ordinary unit tests. Images are newly created files, never physical storage.
func TestFilesystemImages(t *testing.T) {
	if os.Getenv("DASH_FILESYSTEM_TEST") != "1" {
		t.Skip("make filesystem-smoke on a Linux build host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("image test requires an isolated root mount namespace")
	}
	tools := os.Getenv("DASH_FILESYSTEM_TOOLS")
	if !filepath.IsAbs(tools) {
		t.Fatal("absolute bundle path required")
	}
	for _, kind := range []string{"ext4", "exfat", "ntfs"} {
		t.Run(kind, func(t *testing.T) {
			work := t.TempDir()
			image := filepath.Join(work, "disk.img")
			point := filepath.Join(work, "volume")
			f, err := os.Create(image)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(128 << 20); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if err = os.Mkdir(point, 0755); err != nil {
				t.Fatal(err)
			}
			command := func(name string, args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				data, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v %s", name, err, data)
				}
				return data
			}
			switch kind {
			case "ext4":
				command("mkfs.ext4", "-q", "-F", image)
			case "exfat":
				command("mkfs.exfat", image)
			case "ntfs":
				command("mkntfs", "-F", "-Q", image)
			}
			var child *exec.Cmd
			var processDone chan error
			var stderr bytes.Buffer
			loop := ""
			mount := func() {
				t.Helper()
				// exFAT requests fuseblk, which requires a block device just as
				// production does. A regular image file is not a valid substitute.
				loop = string(bytes.TrimSpace(command("losetup", "--find", "--show", image)))
				if kind == "ext4" {
					if err := unix.Mount(loop, point, "ext4", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, ""); err != nil {
						t.Fatal(err)
					}
					return
				}
				name := "mount.exfat-fuse"
				options := "uid=1000,gid=1000,umask=0077,allow_other,default_permissions,nodev,nosuid,noexec,max_read=131072"
				if kind == "ntfs" {
					name = "ntfs-3g"
					options += ",no_detach,norecover,windows_names"
				}
				stderr.Reset()
				child = exec.Command(filepath.Join(tools, name), "-o", options, loop, point)
				child.Stderr = &stderr
				if err := child.Start(); err != nil {
					t.Fatal(err)
				}
				processDone = make(chan error, 1)
				go func() { processDone <- child.Wait() }()
				for i := 0; i < 100; i++ {
					var a, b unix.Stat_t
					if unix.Stat(point, &a) == nil && unix.Stat(work, &b) == nil && a.Dev != b.Dev {
						return
					}
					select {
					case err := <-processDone:
						t.Fatalf("mount: %v %s", err, stderr.String())
					case <-time.After(50 * time.Millisecond):
					}
				}
				t.Fatal("mount timeout")
			}
			unmount := func() {
				t.Helper()
				if err := unix.Unmount(point, 0); err != nil {
					t.Fatal(err)
				}
				if child != nil {
					select {
					case <-processDone:
					case <-time.After(3 * time.Second):
						child.Process.Kill()
						<-processDone
						t.Error("helper did not stop after unmount")
					}
					child = nil
				}
				if loop != "" {
					command("losetup", "-d", loop)
					loop = ""
				}
			}
			mount()
			mounted := true
			defer func() {
				if mounted {
					unmount()
				}
			}()
			// Exercise the same folder ownership and fsync operations as the
			// privileged helper, including exFAT's emulated uid/gid behavior.
			prefix := "Drives/2026"
			for _, relative := range []string{"Drives", prefix, prefix + "/dash-archive"} {
				directory := filepath.Join(point, relative)
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(directory, 1000, 1000); err != nil {
					t.Fatal(err)
				}
				folder, err := os.Open(directory)
				if err != nil {
					t.Fatal(err)
				}
				err = folder.Sync()
				folder.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			data := bytes.Repeat([]byte("original camera bytes"), 700000)
			sum, sha1, err := Hash(context.Background(), bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			a := Asset{Path: "video", Key: "dash-archive/v1/source/sessions/trip/originals/segment/road.hevc", Size: int64(len(data)), Modified: oneSourceModified(), SHA256: sum, SHA1: sha1}
			store, err := NewLocalStoreAt(point, memorySource{data}, "first-process", prefix)
			if err != nil {
				t.Fatal(err)
			}
			cp, done, err := store.Step(context.Background(), a)
			if err != nil || done {
				t.Fatal(done, err)
			}
			// The written chunk survives a clean disconnect/reconnect with the old
			// SQLite checkpoint, just as after a power interruption before its commit.
			if cp.Offset != ChunkBytes {
				t.Fatal(cp.Offset)
			}
			store.Close()
			unmount()
			mounted = false
			mount()
			mounted = true
			store, err = NewLocalStoreAt(point, memorySource{data}, "new-process", prefix)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for i := 0; i < 5; i++ {
				cp, done, err = store.Step(context.Background(), a)
				if err != nil {
					t.Fatal(err)
				}
				a.Checkpoint = cp
				if done {
					break
				}
			}
			if !done {
				t.Fatal("copy did not finish")
			}
			file, err := os.Open(filepath.Join(point, prefix, filepath.FromSlash(a.Key)))
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(file)
			file.Close()
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("read-back mismatch", err)
			}
			// Exercise the publish syscall directly on this filesystem. A check-then-
			// rename in application code would not cover upstream FUSE rename flags.
			pending, err := store.root.Open(store.key("dash-archive/pending"), os.O_CREATE|os.O_WRONLY)
			if err != nil {
				t.Fatal(err)
			}
			pending.WriteString("must not replace original")
			pending.Close()
			if err = store.root.Publish(store.key("dash-archive/pending"), store.key(a.Key)); !errors.Is(err, os.ErrExist) {
				t.Fatalf("exclusive publication failed: %v", err)
			}
			got, err = os.ReadFile(filepath.Join(point, prefix, filepath.FromSlash(a.Key)))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("existing original overwritten", err)
			}
			store.Close()
		})
	}
}
