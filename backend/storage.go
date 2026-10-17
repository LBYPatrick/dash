package main

import (
	"errors"
	"io"

	"golang.org/x/sys/unix"
)

var errRecordingReserve = errors.New("insufficient space above the recording reserve; download directly instead of caching an export")

func recordingSpaceBudget(total, available uint64) uint64 {
	reserve := max(uint64(RecordingReserveBytes), total*RecordingReservePercent/100) + RecordingReserveMargin
	if available <= reserve {
		return 0
	}
	return available - reserve
}

func requireRecordingSpace(path string, additional int64) error {
	var disk unix.Statfs_t
	if err := unix.Statfs(path, &disk); err != nil {
		return err
	}
	budget := recordingSpaceBudget(uint64(disk.Blocks)*uint64(disk.Bsize), uint64(disk.Bavail)*uint64(disk.Bsize))
	if additional < 0 || budget == 0 || uint64(additional) > budget {
		return errRecordingReserve
	}
	return nil
}

// Check before the first write and every 8 MiB thereafter. The margin absorbs
// bounded writes and other app activity between checks without a stat per frame.
type recordingSpaceWriter struct {
	path    string
	writer  io.Writer
	pending int64
}

func (w *recordingSpaceWriter) Write(p []byte) (int, error) {
	if w.pending <= 0 || int64(len(p)) > w.pending {
		if err := requireRecordingSpace(w.path, int64(len(p))); err != nil {
			return 0, err
		}
		w.pending = 8 << 20
	}
	n, err := w.writer.Write(p)
	w.pending -= int64(n)
	return n, err
}
