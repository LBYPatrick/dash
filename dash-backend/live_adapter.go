package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed live_adapter.py
var liveAdapterSource []byte

var liveLeasePattern = regexp.MustCompile(`^[a-f0-9]{32,64}$`)

const firmwarePython = "/usr/local/venv/bin/python"

func (s *Server) localLiveAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	for _, path := range []string{firmwarePython, filepath.Join(s.firmware, "system/camerad/camerad"), filepath.Join(s.firmware, "system/webrtc/webrtcd.py")} {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func liveLease(r *http.Request) string {
	var body struct {
		Lease string `json:"lease"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		return ""
	}
	if !liveLeasePattern.MatchString(body.Lease) {
		return ""
	}
	return body.Lease
}

// A session owns only this worker's process group. The firmware manager,
// processes, files and driving params are never modified by the local adapter.
func (s *Server) startLocalLive(w http.ResponseWriter, r *http.Request, sdp, camera, lease string) {
	address := ""
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		address, _, _ = net.SplitHostPort(local.String())
	}
	if net.ParseIP(address) == nil {
		fail(w, 400, errors.New("local streaming requires a direct LAN connection"))
		return
	}
	wait, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	release, err := acquireCompute(wait)
	if err != nil {
		fail(w, 409, err)
		return
	}
	s.liveMu.Lock()
	if s.liveOwned || s.index.state() != "parked" {
		s.liveMu.Unlock()
		release()
		fail(w, 409, errors.New("live camera busy or vehicle not parked"))
		return
	}
	script, err := os.CreateTemp(s.index.cache, ".live-adapter-*.py")
	if err != nil {
		s.liveMu.Unlock()
		release()
		fail(w, 500, err)
		return
	}
	name := script.Name()
	_, err = script.Write(liveAdapterSource)
	closeErr := script.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		s.liveMu.Unlock()
		release()
		fail(w, 500, err)
		return
	}
	command := exec.Command(firmwarePython, "-B", "-u", name)
	command.Dir = s.firmware
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+s.firmware, "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr limitedBuffer
	// stderr is written by exec's copier; inspect only after Wait returns.
	command.Stderr = &stderr
	input, inputErr := command.StdinPipe()
	output, outputErr := command.StdoutPipe()
	if inputErr != nil || outputErr != nil {
		os.Remove(name)
		s.liveMu.Unlock()
		release()
		fail(w, 500, errors.New("live worker pipes unavailable"))
		return
	}
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		os.Remove(name)
		s.liveMu.Unlock()
		release()
		fail(w, 500, err)
		return
	}
	log.Printf("Dash live worker started pid=%d", command.Process.Pid)
	done := make(chan struct{})
	s.liveCancel = func() {
		input.Close()
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}
	s.liveOwned = true
	s.liveLease = lease
	s.liveTouched = time.Now()
	s.liveRelease = release
	s.liveMu.Unlock()
	go func() {
		err := command.Wait()
		os.Remove(name)
		log.Printf("Dash live worker stopped pid=%d error=%v detail=%s", command.Process.Pid, err, strings.TrimSpace(stderr.String()))
		close(done)
		s.stopLiveLease(lease)
	}()
	config := map[string]any{"address": address, "sdp": sdp, "camera": camera, "firmware": s.firmware, "params": s.params, "threads": MediaThreads, "fps": LiveFPS, "width": LiveMaxWidth, "height": LiveMaxHeight}
	if err := json.NewEncoder(input).Encode(config); err != nil {
		s.stopLiveLease(lease)
		fail(w, 502, err)
		return
	}
	answer := make(chan []byte, 1)
	readError := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(output, 1<<20))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var candidate struct {
				Type string `json:"type"`
				SDP  string `json:"sdp"`
			}
			if json.Unmarshal(scanner.Bytes(), &candidate) == nil && candidate.Type == "answer" && candidate.SDP != "" {
				answer <- append([]byte(nil), scanner.Bytes()...)
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = errors.New("live camera worker exited before answering")
		}
		readError <- err
	}()

	timeout := time.NewTimer(40 * time.Second)
	defer timeout.Stop()
	select {
	case data := <-answer:
		var parsed struct {
			SDP  string `json:"sdp"`
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &parsed) != nil || parsed.Type != "answer" || parsed.SDP == "" {
			s.stopLiveLease(lease)
			fail(w, 502, errors.New("invalid live worker answer"))
			return
		}
		writeJSON(w, 200, parsed)
	case err := <-readError:
		s.stopLiveLease(lease)
		fail(w, 502, err)
	case <-r.Context().Done():
		s.stopLiveLease(lease)
	case <-timeout.C:
		s.stopLiveLease(lease)
		fail(w, 504, errors.New("camera startup timed out"))
	}
}
