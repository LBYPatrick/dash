package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"

	"dash.local/backend/internal/archivepath"
	"golang.org/x/sys/unix"
)

type LocalStore struct {
	root   *SafeDir
	source Source
	epoch  string
	prefix string
}

func NewLocalStore(root string, source Source, epoch string) (*LocalStore, error) {
	return NewLocalStoreAt(root, source, epoch, "")
}
func NewLocalStoreAt(root string, source Source, epoch, prefix string) (*LocalStore, error) {
	if err := archivepath.Validate(prefix); err != nil {
		return nil, err
	}
	directory, err := OpenDir(root)
	if err != nil {
		return nil, ErrDisconnected
	}
	return &LocalStore{root: directory, source: source, epoch: epoch, prefix: prefix}, nil
}
func (s *LocalStore) key(key string) string { return path.Join(s.prefix, key) }
func (s *LocalStore) Close() error          { return s.root.Close() }
func partialKey(key, hash string) string {
	digest := sha256.Sum256([]byte(key + ":" + hash))
	parts := strings.Split(key, "/")
	base := "dash-archive"
	if len(parts) > 3 {
		base = path.Join(parts[:3]...)
	}
	return path.Join(base, ".partial", hex.EncodeToString(digest[:])+".part")
}
func (s *LocalStore) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	cp := a.Checkpoint
	if file, err := s.root.Open(s.key(a.Key), os.O_RDONLY); err == nil {
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return cp, false, err
		}
		if !info.Mode().IsRegular() || info.Size() != a.Size {
			return cp, false, ErrConflict
		}
		hash, _, err := Hash(ctx, file)
		if err != nil {
			return cp, false, err
		}
		if hash != a.SHA256 {
			return cp, false, ErrConflict
		}
		cp.Reused = cp.SentBytes == 0
		return completed(cp, a.Size), true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return cp, false, err
	}
	source, err := s.source.Open(a.Path)
	if err != nil {
		return cp, false, ErrMissing
	}
	defer source.Close()
	if !unchanged(source, a) {
		return cp, false, ErrMissing
	}
	temporary := partialKey(a.Key, a.SHA256)
	file, err := s.root.Open(s.key(temporary), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return cp, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return cp, false, ErrConflict
	}
	if info.Size() > a.Size {
		return cp, false, ErrConflict
	}
	if cp.VerifiedEpoch != s.epoch || cp.Offset != info.Size() {
		// After power loss the destination is authoritative only after read-back.
		left, _, err := Hash(ctx, io.LimitReader(file, info.Size()))
		if err != nil {
			return cp, false, err
		}
		right, _, err := Hash(ctx, io.NewSectionReader(source.ReaderAt, 0, info.Size()))
		if err != nil {
			return cp, false, err
		}
		if left != right {
			return cp, false, errors.New("partial copy failed verification")
		}
		cp.SentBytes += max(int64(0), info.Size()-cp.Offset)
		cp.Offset, cp.VerifiedEpoch = info.Size(), s.epoch
	}
	if cp.Offset < a.Size {
		var disk unix.Statfs_t
		if err = unix.Fstatfs(int(file.Fd()), &disk); err != nil {
			return cp, false, err
		}
		count := min(ChunkBytes, a.Size-cp.Offset)
		if uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(count)+USBReserveBytes {
			return cp, false, ErrFull
		}
		if _, err = file.Seek(cp.Offset, io.SeekStart); err != nil {
			return cp, false, err
		}
		copied, err := io.CopyBuffer(file, contextReader{ctx, io.NewSectionReader(source.ReaderAt, cp.Offset, count)}, make([]byte, CopyBufferBytes))
		if err != nil {
			return cp, false, err
		}
		if copied != count {
			return cp, false, ErrMissing
		}
		if err = file.Sync(); err != nil {
			return cp, false, err
		}
		cp.Offset += copied
		cp.SentBytes += copied
		return cp, false, nil
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return cp, false, err
	}
	hash, _, err := Hash(ctx, file)
	if err != nil {
		return cp, false, err
	}
	if hash != a.SHA256 {
		return cp, false, errors.New("USB read-back verification failed")
	}
	if err = file.Sync(); err != nil {
		return cp, false, err
	}
	if err = file.Close(); err != nil {
		return cp, false, err
	}
	if err = s.root.Publish(s.key(temporary), s.key(a.Key)); errors.Is(err, os.ErrExist) {
		return cp, false, ErrConflict
	} else if err != nil {
		return cp, false, err
	}
	return completed(cp, a.Size), true, nil
}
func (s *LocalStore) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	if existing, err := s.root.Open(s.key(key), os.O_RDONLY); err == nil {
		defer existing.Close()
		hash, _, err := Hash(ctx, existing)
		if err != nil {
			return err
		}
		if hash != expected {
			return ErrConflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := partialKey(key, expected)
	file, err := s.root.Open(s.key(temporary), os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, contextReader{ctx, bytes.NewReader(data)})
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return s.root.Publish(s.key(temporary), s.key(key))
}
func (s *LocalStore) Abort(ctx context.Context, a Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.SHA256 == "" {
		return nil
	}
	err := s.root.Remove(s.key(partialKey(a.Key, a.SHA256)))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
