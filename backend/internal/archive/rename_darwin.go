package archive

import "golang.org/x/sys/unix"

func renameExclusive(old int, oldName string, next int, newName string) error {
	return unix.RenameatxNp(old, oldName, next, newName, unix.RENAME_EXCL)
}
