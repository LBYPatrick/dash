package archive

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// SafeDir pins a directory descriptor. Every child component is opened without
// following symlinks; kernel 4.9 does not provide openat2. An unplugged mount can
// fail, but cannot redirect an existing descriptor into internal storage.
type SafeDir struct {
	mu sync.Mutex
	fd int
}

func OpenDir(name string) (*SafeDir, error) {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &SafeDir{fd: fd}, nil
}
func (d *SafeDir) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fd < 0 {
		return nil
	}
	err := unix.Close(d.fd)
	d.fd = -1
	return err
}
func validRelative(name string) bool {
	return name != "" && name != "." && !strings.HasPrefix(name, "/") && path.Clean(name) == name && !strings.ContainsAny(name, "\\\x00\r\n") && name != ".." && !strings.HasPrefix(name, "../")
}
func (d *SafeDir) parent(name string, create bool) (int, string, error) {
	if !validRelative(name) {
		return -1, "", errors.New("unsafe archive path")
	}
	d.mu.Lock()
	if d.fd < 0 {
		d.mu.Unlock()
		return -1, "", os.ErrClosed
	}
	fd, err := unix.FcntlInt(uintptr(d.fd), unix.F_DUPFD_CLOEXEC, 0)
	d.mu.Unlock()
	if err != nil {
		return -1, "", err
	}
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if create {
			if err = unix.Mkdirat(fd, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				unix.Close(fd)
				return -1, "", err
			}
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return -1, "", e
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}
func (d *SafeDir) Open(name string, flags int) (*os.File, error) {
	fd, base, err := d.parent(name, flags&os.O_CREATE != 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	file, err := unix.Openat(fd, base, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(file), base), nil
}
func (d *SafeDir) Remove(name string) error {
	fd, base, err := d.parent(name, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Unlinkat(fd, base, 0)
}
func (d *SafeDir) Publish(from, to string) error {
	old, oldName, err := d.parent(from, false)
	if err != nil {
		return err
	}
	defer unix.Close(old)
	next, newName, err := d.parent(to, true)
	if err != nil {
		return err
	}
	defer unix.Close(next)
	if err = renameExclusive(old, oldName, next, newName); err != nil {
		return err
	}
	if err = unix.Fsync(next); err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return nil
}
func (d *SafeDir) OpenSource(name string) (*SourceFile, error) {
	f, err := d.Open(name, os.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrMissing
	}
	return &SourceFile{ReadSeekCloser: f, ReaderAt: f, Size: info.Size(), Modified: info.ModTime()}, nil
}

type directorySource struct{ *SafeDir }

func (d directorySource) Open(name string) (*SourceFile, error) { return d.OpenSource(name) }

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
func Hash(ctx context.Context, r io.Reader) (string, string, error) {
	sha, one := sha256.New(), sha1.New()
	_, err := io.CopyBuffer(io.MultiWriter(sha, one), contextReader{ctx, r}, make([]byte, 256<<10))
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(sha.Sum(nil)), hex.EncodeToString(one.Sum(nil)), nil
}
func unchanged(file *SourceFile, asset Asset) bool {
	return file.Size == asset.Size && file.Modified.UnixNano() == asset.Modified
}
