package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsBoundedRotationAndRestart(t *testing.T) {
	directory := t.TempDir()
	logs, err := newDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 600; n++ {
		if _, err := logs.Write([]byte(strings.Repeat("x", 16000) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := logs.Write([]byte("most recent failure\n")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 3 {
		t.Fatal("unbounded log files")
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Size() > logFileLimit {
			t.Fatal("log exceeds limit")
		}
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	logs, err = newDiagnostics(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	report := logs.recent()
	if len(report) > 2*diagnosticTailLimit+1024 || !strings.Contains(report, "most recent failure") {
		t.Fatal("missing or unbounded recent logs")
	}
}
func TestDiagnosticsDownloadWorksOnroadAndRejectsForeignOrigin(t *testing.T) {
	index, parked := fixtureIndex(t)
	parked.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, _ := newExports(ctx, index)
	server := newServer(ctx, index, exports, t.TempDir(), t.TempDir(), t.TempDir(), "http://127.0.0.1:1")
	logs, err := newDiagnostics(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	server.diagnostics = logs
	_, _ = logs.Write([]byte("processing stopped: canceled or vehicle no longer confirmed parked\n"))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	response, err := http.Get(httpServer.URL + "/api/diagnostics/download")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(data), "Driving state: onroad") || !strings.Contains(string(data), "processing stopped") {
		t.Fatal("onroad diagnostic missing")
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), "attachment;") || len(data) > 2<<20 {
		t.Fatal("invalid bounded download")
	}
	if strings.Contains(string(data), "latitude") || strings.Contains(string(data), "TEST VEHICLE") {
		t.Fatal("telemetry leaked")
	}
	request, _ := http.NewRequest("GET", httpServer.URL+"/api/diagnostics/download", nil)
	request.Header.Set("Origin", "https://foreign.example")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("foreign origin allowed")
	}
}
func TestSimulationNeverAvailableInProduction(t *testing.T) {
	index, _ := fixtureIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, _ := newExports(ctx, index)
	params := t.TempDir()
	server := newServer(ctx, index, exports, t.TempDir(), params, t.TempDir(), "")
	call := func(state string) int {
		request := httptest.NewRequest("POST", "/api/simulation/state", strings.NewReader(`{"state":"`+state+`"}`))
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code
	}
	if call("parked") != 404 {
		t.Fatal("production exposes driving-state mutation")
	}
	server.simulation = true
	for _, state := range []string{"parked", "onroad", "unknown"} {
		if call(state) != 200 || paramsState(params)() != state {
			t.Fatal("simulation state mismatch", state)
		}
	}
	if call("invalid") != 400 {
		t.Fatal("invalid simulation state accepted")
	}
	if _, err := os.Stat(filepath.Join(params, "IsOnroad")); !os.IsNotExist(err) {
		t.Fatal("unknown retains IsOnroad")
	}
}
