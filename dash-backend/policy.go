package main

import "time"

// Fixed comma 3X/4 budgets. Tune these together; no device/controller discovery
// or CPU affinity is used. One heavy operation owns the media thread budget.
const (
	DeviceCPUCores          = 8
	MediaThreads            = DeviceCPUCores
	ProbeThreads            = 1
	HeavyJobLimit           = 1
	GoHeapTargetMiB         = 96
	SupervisorHeapTargetMiB = 24
	MediaAllocationLimitMiB = 32
	MediaRSSLimitMiB        = 192 // Optional host/container instrumentation only.
	ServiceMemoryHighMiB    = 256
	ServiceMemoryMaxMiB     = 384
	HostRAMReserveMiB       = 512 // Optional host/container instrumentation only.
	LiveFPS                 = 10
	LiveMaxWidth            = 640
	LiveMaxHeight           = 360
	LiveLeaseTimeout        = 12 * time.Second
	// One IDR per message permits arbitrary paused seeks in Foxglove.
	MCAPKeyframeIntervalFrames = 1
	PreviewChunkSeconds        = 4
	PreviewMaxWidth            = 960
	PreviewMaxHeight           = 540
	DrivingWatchInterval       = 100 * time.Millisecond
	IndexInterval              = 15 * time.Second
)

// Set once before workers start. Device mode uses fixed budgets and never
// inspects /proc, cgroup controllers, system RAM or the CPU topology.
var deviceMode bool
