package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"
)

func main() {
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
	flag.Parse()
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
		server := &Server{params: *params}
		if _, err := os.Stat(filepath.Join(*params, ".dash-live-owned")); err == nil {
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
	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(128 << 20)
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
		log.Fatal("PWA build missing; run npm run build and supply --app dist")
	}
	if (*cert == "") != (*key == "") {
		log.Fatal("--cert and --key are required together")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	index, err := newIndex(rootPath, *cache, paramsState(*params))
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
	go func() {
		ticker := time.NewTicker(15 * time.Second)
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
	log.Printf("dash-backend on %s; state=%s; heavy jobs=1; Go heap target=128 MiB", *address, index.state())
	if *cert != "" {
		err = httpServer.ListenAndServeTLS(*cert, *key)
	} else {
		err = httpServer.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
