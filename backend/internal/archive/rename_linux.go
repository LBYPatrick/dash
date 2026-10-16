package archive

import (
	"errors"
	"golang.org/x/sys/unix"
)

func renameExclusive(old int, oldName string, next int, newName string) error {
	err := unix.Renameat2(old, oldName, next, newName, unix.RENAME_NOREPLACE)
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	// Legacy NTFS/FUSE lacks rename2. A hard link publishes exclusively too;
	// filesystems supporting neither operation fail closed, never overwrite.
	if err = unix.Linkat(old, oldName, next, newName, 0); err != nil {
		return err
	}
	return unix.Unlinkat(old, oldName, 0)
}
