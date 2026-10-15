package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
)

// Only the explicit developer simulation mode can modify driving-state params.
// Production device units never enable this mode.
func (s *Server) simulationState(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if !s.simulation {
		http.NotFound(w, r)
		return
	}
	var request struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil {
		fail(w, 400, err)
		return
	}
	if request.State != "parked" && request.State != "onroad" && request.State != "unknown" {
		fail(w, 400, errors.New("state must be parked, onroad, or unknown"))
		return
	}
	s.simulationMu.Lock()
	defer s.simulationMu.Unlock()
	// Invalidate offroad first so intermediate states fail closed.
	if err := os.WriteFile(filepath.Join(s.params, "IsOffroad"), []byte("0"), 0600); err != nil {
		fail(w, 500, err)
		return
	}
	if request.State == "unknown" {
		for _, key := range []string{"IsOffroad", "IsOnroad"} {
			if err := os.Remove(filepath.Join(s.params, key)); err != nil && !os.IsNotExist(err) {
				fail(w, 500, err)
				return
			}
		}
	} else {
		on, off := "1", "0"
		if request.State == "parked" {
			on, off = "0", "1"
		}
		if err := os.WriteFile(filepath.Join(s.params, "IsOnroad"), []byte(on), 0600); err != nil {
			fail(w, 500, err)
			return
		}
		if err := os.WriteFile(filepath.Join(s.params, "IsOffroad"), []byte(off), 0600); err != nil {
			fail(w, 500, err)
			return
		}
	}
	writeJSON(w, 200, map[string]string{"driving": s.index.state()})
}
