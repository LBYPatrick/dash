package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
)

// One compute job across indexing, previews, and exports. HTTP file streaming
// doesn't take this slot. The firmware retains the rest of the CPU cores.
var computeSlot = make(chan struct{}, 1)

func acquireCompute(ctx context.Context) (func(), error) {
	if !memoryAvailable() {
		return nil, errors.New("processing paused: less than 512 MiB system memory available")
	}
	select {
	case computeSlot <- struct{}{}:
		if ctx.Err() != nil {
			<-computeSlot
			return nil, ctx.Err()
		}
		return func() { <-computeSlot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func procKB(filename, key string) int64 {
	file, err := os.Open(filename)
	if err != nil {
		return -1
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) > 1 && parts[0] == key {
			value, _ := strconv.ParseInt(parts[1], 10, 64)
			return value
		}
	}
	return -1
}
func memoryAvailable() bool {
	available := procKB("/proc/meminfo", "MemAvailable:")
	return available < 0 || available >= 512*1024
}
