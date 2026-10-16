package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"dash.local/backend/internal/archive"
	"dash.local/backend/internal/usb"
)

func main() {
	device := flag.Bool("device", false, "Fixed comma 3X/4 budgets; bundled tools; no system/controller probing")
	computeGate := flag.String("compute-gate", "", "Optional Dash-owned test gate; actual firmware parked state is always required")
	resourceSettings := flag.Bool("resource-settings", false, "Print fixed application resource budgets and exit")
	root := flag.String("root", "/data/media/0/realdata", "Read-only recording directory")
	cache := flag.String("cache", "/data/dash/cache", "Disposable SQLite, preview and export cache")
	params := flag.String("params", "/data/params/d", "Firmware params directory; unknown state disables compute")
	app := flag.String("app", "dist", "Required built Vue PWA directory")
	address := flag.String("listen", "127.0.0.1:8088", "HTTP listen address")
	cert := flag.String("cert", "", "Trusted HTTPS certificate")
	key := flag.String("key", "", "HTTPS private key")
	firmware := flag.String("firmware", "/data/openpilot", "Firmware directory, used to detect managed WebRTC support")
	upstream := flag.String("webrtc", "http://127.0.0.1:5001", "Local firmware WebRTC daemon")
	requiredLimits := flag.Bool("require-systemd-limits", false, "Refuse startup without verified systemd cgroup v2 resource controls")
	stopLive := flag.Bool("stop-live", false, "Clear a Dash-owned firmware live session without starting the server")
	simulation := flag.Bool("simulation", false, "Developer sandbox: allow synthetic driving-state changes; never enable on a device")
	containerLimits := flag.Bool("require-container-limits", false, "Verify Docker CPU, memory, swap and PID controls")
	healthCheck := flag.Bool("health-check", false, "Check local HTTP readiness without starting workers")
	supervise := flag.Bool("supervise", false, "Run the installed release with startup health checks and rollback")
	updateHome := flag.String("update-home", "", "Managed release installation directory")
	readyFile := flag.String("ready-file", "", "Internal supervisor readiness marker")
	version := flag.Bool("version", false, "Print build identity as JSON and exit")
	installRelease := flag.Bool("install-release", false, "Install a verified release on this parked device; no server or workers")
	installChannel := flag.String("install-channel", "main", "Installation channel: main, nightly or dev")
	installTag := flag.String("install-tag", "", "Exact immutable installation tag")
	installRepository := flag.String("install-repo", "LBYPatrick/dash", "Public installation repository")
	installRuntime := flag.Bool("install-runtime", false, "Register only until reboot")
	transferHome := flag.String("transfer-home", "", "Durable transfer state; defaults to transfers next to cache")
	configureTransfer := flag.String("configure-transfer", "", "Import a private destination JSON file, or - for stdin")
	transferKey := flag.Bool("transfer-setup-key", false, "Print the private setup key for trusted HTTPS destination configuration")
	authorizeTransfer := flag.String("authorize-transfer", "", "Authorize a configured Google Drive/OneDrive destination over SSH")
	localUSB := flag.String("simulation-usb-root", "", "Synthetic USB archive directory; simulation only")
	usbHelper := flag.Bool("usb-helper", false, "Internal root-owned USB mount service")
	verifyUSB := flag.Bool("verify-usb-bundle", false, "Verify bundled static USB filesystem tools without mounting")
	flag.Parse()
	if *verifyUSB {
		executable, err := os.Executable()
		if err == nil {
			err = verifyStaticTools(filepath.Join(filepath.Dir(executable), "bin/usb"), runtime.GOARCH, []string{"mount.exfat-fuse", "ntfs-3g"})
		}
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	if *transferHome == "" {
		*transferHome = filepath.Join(filepath.Dir(*cache), "transfers")
	}
	if *configureTransfer != "" || *transferKey || *authorizeTransfer != "" {
		if err := transferCLI(*transferHome, *configureTransfer, *transferKey, *authorizeTransfer, paramsState(*params)); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *usbHelper {
		runtime.GOMAXPROCS(1)
		debug.SetMemoryLimit(USBHeapTargetMiB << 20)
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		state := func() string {
			data, err := os.ReadFile("/run/dash/boot-validated")
			if err != nil || strings.TrimSpace(string(data)) != "validated" {
				return "unknown"
			}
			return paramsState("/data/params/d")()
		}
		if err := usb.Serve(ctx, state); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *installRelease {
		if err := installDeviceRelease(*installRepository, *installChannel, *installTag, *installRuntime); err != nil {
			log.Fatal(err)
		}
		return
	}
	deviceMode = *device
	if *resourceSettings {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]int{"cpuCores": DeviceCPUCores, "mediaThreads": MediaThreads, "probeThreads": ProbeThreads, "idleGoProcs": IdleGoProcs, "heavyJobs": HeavyJobLimit, "heapMiB": GoHeapTargetMiB, "memoryHighMiB": ServiceMemoryHighMiB, "memoryMaxMiB": ServiceMemoryMaxMiB, "usbMemoryHighMiB": USBMemoryHighMiB, "usbMemoryMaxMiB": USBMemoryMaxMiB})
		return
	}
	state := paramsState(*params)
	if *computeGate != "" {
		state = combinedState(state, paramsState(*computeGate))
	}
	if *device && (*simulation || *requiredLimits || *containerLimits) {
		log.Fatal("device mode cannot be combined with simulation or controller probing")
	}
	if *device && !*supervise && os.Getenv("DASH_SUPERVISED") != "1" && !*stopLive {
		log.Fatal("device mode requires the managed Dash supervisor")
	}

	if *version {
		_ = json.NewEncoder(os.Stdout).Encode(currentBuild())
		return
	}
	if *supervise {
		if *updateHome == "" {
			log.Fatal("--supervise requires --update-home")
		}
		if *requiredLimits {
			if err := verifySystemdLimits(); err != nil {
				log.Fatal(err)
			}
		}
		if *containerLimits {
			directory, err := currentCgroup()
			if err != nil {
				log.Fatal(err)
			}
			if err := verifyKernelLimits(directory, false); err != nil {
				log.Fatal(err)
			}
		}
		if !*requiredLimits && !*containerLimits && !*device {
			log.Fatal("supervisor requires verified systemd or container limits")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := runSupervisor(ctx, *updateHome, state, supervisorArgs(os.Args[1:])); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *updateHome != "" && os.Getenv("DASH_SUPERVISED") != "1" {
		log.Fatal("managed updates require the installed supervisor")
	}
	if *healthCheck {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://127.0.0.1:8088/api/info")
		if err != nil {
			log.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			log.Fatal("backend not ready")
		}
		return
	}
	if *stopLive {
		server := &Server{params: *params, index: &Index{cache: *cache}}
		if _, err := os.Stat(filepath.Join(*cache, ".dash-live-owned")); err == nil {
			if err := server.setLive(false); err != nil {
				log.Fatal(err)
			}
		}
		return
	}
	diagnostics, err := setupLogging(*cache)
	if err != nil {
		log.Fatal("diagnostic logging: ", err)
	}
	defer diagnostics.Close()
	if *simulation && *requiredLimits {
		log.Fatal("simulation is incompatible with the device systemd mode")
	}
	if *localUSB != "" && !*simulation {
		log.Fatal("synthetic USB storage requires simulation mode")
	}
	if *containerLimits {
		directory, err := currentCgroup()
		if err != nil {
			log.Fatal(err)
		}
		if err := verifyKernelLimits(directory, false); err != nil {
			log.Fatal("container resource verification: ", err)
		}
		log.Print("verified container limits: CPU <=50% of one core, memory <=384 MiB, swap=0, tasks <=64")
	}
	if *requiredLimits {
		if err := verifySystemdLimits(); err != nil {
			log.Fatal("resource verification: ", err)
		}
		log.Print("verified kernel controls: CPU <=50% of one core, memory <=384 MiB, high <=256 MiB, swap=0, tasks <=64")
	}
	if *device {
		for _, tool := range []string{"ffmpeg", "ffprobe"} {
			if _, err := mediaTool(tool); err != nil {
				log.Fatal(err)
			}
		}
	}
	if *device {
		runtime.GOMAXPROCS(goParallelism(state()))
	} else {
		runtime.GOMAXPROCS(DeviceCPUCores)
	}
	debug.SetMemoryLimit(GoHeapTargetMiB << 20)
	debug.SetGCPercent(60)
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	appPath, err := filepath.Abs(*app)
	if err != nil {
		log.Fatal(err)
	}
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		log.Fatal("recording root is not a directory")
	}
	if _, err := os.Stat(filepath.Join(appPath, "index.html")); err != nil {
		log.Fatal("PWA build missing; run make build and supply --app frontend/dist from the repository root")
	}
	if (*cert == "") != (*key == "") {
		log.Fatal("--cert and --key are required together")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	index, err := newIndex(rootPath, *cache, state)
	if err != nil {
		log.Fatal(err)
	}
	defer index.db.Close()
	exports, err := newExports(ctx, index)
	if err != nil {
		log.Fatal(err)
	}
	server := newServer(ctx, index, exports, appPath, *params, *firmware, *upstream)
	server.diagnostics = diagnostics
	server.simulation = *simulation
	server.transferHome, server.transferLocalRoot, server.transferEpoch = *transferHome, *localUSB, newTransferID()
	server.usbClient = usb.NewClient()
	if *localUSB != "" {
		if err := os.MkdirAll(*localUSB, 0700); err != nil {
			log.Fatal(err)
		}
	}
	transfers, err := archive.New(ctx, archive.Options{Root: rootPath, Home: *transferHome, State: state, Acquire: acquireCompute, Factory: server.newTransferStore})
	if err != nil {
		log.Print("Transfer state unavailable; retained for repair. Playback and existing features remain available.")
	} else {
		defer transfers.Close()
		server.transfers = transfers
		server.transferAuth = archive.NewAuthorizer(ctx, transfers.Configs(), state)
	}
	updates, err := newUpdater(ctx, *updateHome, index.state, cancel)
	if err != nil {
		log.Printf("updates unavailable: %v", err)
		updates = &UpdateManager{ctx: ctx, state: index.state, status: UpdateState{Current: currentBuild(), Phase: "unavailable", Message: "Updates are unavailable: " + err.Error() + ". Repair the installation over SSH."}}
	}
	server.updates = updates
	go updates.automaticChecks()
	go func() {
		ticker := time.NewTicker(IndexInterval)
		defer ticker.Stop()
		for {
			if index.state() == "parked" && !exports.busy() {
				if err := index.scan(ctx); err != nil && ctx.Err() == nil {
					log.Printf("index: %v", err)
				}
				exports.cleanup()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	httpServer := &http.Server{Addr: *address, Handler: server, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Printf("dash-backend on %s; state=%s; heavy jobs=1; Go heap target=%d MiB; media threads=%d", *address, index.state(), GoHeapTargetMiB, MediaThreads)
	listener, listenErr := net.Listen("tcp", *address)
	if listenErr != nil {
		log.Fatal(listenErr)
	}
	if *readyFile != "" {
		if *updateHome == "" || filepath.Dir(*readyFile) != filepath.Join(*updateHome, "cache") {
			log.Fatal("invalid internal readiness path")
		}
		if err := atomicJSON(*readyFile, map[string]any{"pid": os.Getpid(), "revision": buildRevision, "tag": currentBuild().Tag}); err != nil {
			log.Fatal(err)
		}
		defer os.Remove(*readyFile)
	}
	if *cert != "" {
		err = httpServer.ServeTLS(listener, *cert, *key)
	} else {
		err = httpServer.Serve(listener)
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
