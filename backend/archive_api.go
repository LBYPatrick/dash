package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"dash.local/backend/internal/archive"
	"dash.local/backend/internal/usb"
)

func newTransferID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("system random source failed")
	}
	return hex.EncodeToString(value[:])
}
func transferSetupKey(home string) (string, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", err
	}
	file := filepath.Join(home, "setup.key")
	if f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		key := newTransferID()
		_, writeErr := f.WriteString(key + "\n")
		if writeErr == nil {
			writeErr = f.Sync()
		}
		f.Close()
		if writeErr != nil {
			return "", writeErr
		}
	}
	root, err := archive.OpenDir(home)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open("setup.key", os.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if len(key) != 32 {
		return "", errors.New("invalid transfer setup key")
	}
	return key, nil
}
func transferCLI(home, configure string, key bool, authorize string, state StateReader) error {
	configs, err := archive.OpenConfigs(filepath.Join(home, "destinations"))
	if err != nil {
		return err
	}
	if key {
		value, err := transferSetupKey(home)
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	}
	if configure != "" {
		var reader io.Reader = os.Stdin
		if configure != "-" {
			file, err := os.Open(configure)
			if err != nil {
				return errors.New("private destination file could not open")
			}
			defer file.Close()
			reader = file
		}
		var config archive.Config
		decoder := json.NewDecoder(io.LimitReader(reader, 65537))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&config) != nil {
			return errors.New("invalid private destination configuration")
		}
		if err = configs.Save(config); err != nil {
			return err
		}
		fmt.Printf("Saved destination %s. Credentials remain backend-only.\n", config.ID)
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	auth := archive.NewAuthorizer(ctx, configs, state)
	request, err := auth.Begin(ctx, authorize)
	if err != nil {
		return err
	}
	fmt.Printf("Open %s on your phone and enter %s\n", request.VerificationURI, request.UserCode)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			for _, status := range auth.Snapshot() {
				if status.DestinationID == authorize {
					switch status.Status {
					case "connected":
						fmt.Println("Destination connected.")
						return nil
					case "failed", "expired":
						return errors.New("authorization did not complete; check provider registration and retry")
					}
				}
			}
		}
	}
}
func secureTransferSetup(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return r.TLS != nil || (ip != nil && ip.IsLoopback())
}
func (s *Server) canConfigureTransfers(w http.ResponseWriter, r *http.Request) bool {
	if !secureTransferSetup(r) {
		fail(w, 403, errors.New("configure destinations over SSH or trusted HTTPS; credentials cannot be entered over plain LAN HTTP"))
		return false
	}
	key, err := transferSetupKey(s.transferHome)
	supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "DashSetup ")
	if err != nil || subtle.ConstantTimeCompare([]byte(key), []byte(supplied)) != 1 {
		fail(w, 403, errors.New("the private transfer setup key is required"))
		return false
	}
	return true
}
func (s *Server) transferAPI(w http.ResponseWriter, r *http.Request) {
	if s.transfers == nil {
		fail(w, 503, errors.New("batch transfers are unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/transfers":
			batches, err := s.transfers.Snapshot()
			if err != nil {
				fail(w, 500, errors.New("transfer state could not be read"))
				return
			}
			destinations, err := s.transfers.Configs().All()
			if err != nil {
				fail(w, 500, errors.New("destinations could not be read"))
				return
			}
			writeJSON(w, 200, map[string]any{"batches": batches, "destinations": destinations, "authorizations": s.transferAuth.Snapshot(), "driving": s.index.state(), "configurationSecure": secureTransferSetup(r), "usbSupported": runtime.GOOS == "linux" || s.transferLocalRoot != "", "bundledProviders": archive.BundledProviders()})
			return
		case "/api/transfers/volumes":
			if s.transferLocalRoot != "" {
				writeJSON(w, 200, []usb.Volume{{UUID: "DASH-SIM-0001", Label: "Synthetic USB volume", Filesystem: "exfat", Available: true}})
				return
			}
			volumes, err := s.usbClient.Volumes(r.Context())
			if err != nil {
				fail(w, 409, err)
				return
			}
			writeJSON(w, 200, volumes)
			return
		default:
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/transfers/"), "/")
			if len(parts) == 2 && parts[1] == "issues" {
				issues, err := s.transfers.Issues(parts[0])
				if err != nil {
					fail(w, 500, errors.New("transfer issues could not be read"))
					return
				}
				writeJSON(w, 200, issues)
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/api/transfers" {
		var input struct {
			DestinationID string `json:"destinationId"`
			AutoResume    bool   `json:"autoResume"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			fail(w, 400, errors.New("invalid batch request"))
			return
		}
		batch, err := s.transfers.Start(input.DestinationID, input.AutoResume)
		if err != nil {
			fail(w, 409, err)
			return
		}
		writeJSON(w, 202, batch)
		return
	}
	if r.URL.Path == "/api/transfer-destinations" {
		var config archive.Config
		decoder := json.NewDecoder(io.LimitReader(r.Body, 65537))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&config) != nil {
			fail(w, 400, errors.New("invalid private destination configuration"))
			return
		}
		bundled := archive.BundledProviders()[config.Kind] && config.ClientID == "" && config.Secret == (archive.Secret{})
		existing, existingErr := s.transfers.Configs().Load(config.ID)
		if (existingErr == nil && existing.Kind != "usb") || (!bundled && (config.Kind != "usb" || config.Secret != (archive.Secret{}))) {
			if !s.canConfigureTransfers(w, r) {
				return
			}
		}
		if s.transfers.DestinationBusy(config.ID) {
			fail(w, 409, errors.New("finish or cancel this destination's transfer before changing it"))
			return
		}
		if err := s.transfers.Configs().Save(config); err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, config.Public())
		return
	}
	if r.URL.Path == "/api/transfers/eject" {
		var input struct {
			UUID string `json:"uuid"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			fail(w, 400, errors.New("invalid USB eject request"))
			return
		}
		destinations, _ := s.transfers.Configs().All()
		batches, _ := s.transfers.Snapshot()
		for _, destination := range destinations {
			if destination.Kind == "usb" && strings.EqualFold(destination.Location, input.UUID) {
				for _, batch := range batches {
					if batch.DestinationID == destination.ID && batch.Status != "complete" && batch.Status != "complete_with_issues" && batch.Status != "canceled" {
						_ = s.transfers.Action(batch.ID, "pause")
					}
				}
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := s.transfers.WaitIdle(ctx); err != nil {
			fail(w, 409, errors.New("USB transfer is still stopping; try eject again"))
			return
		}
		if s.transferLocalRoot == "" {
			if err := s.usbClient.Eject(ctx, input.UUID); err != nil {
				fail(w, 409, err)
				return
			}
		}
		writeJSON(w, 200, map[string]bool{"ejected": true})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/transfer-destinations/") {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/transfer-destinations/"), "/")
		if len(parts) != 2 || !archive.IDPattern.MatchString(parts[0]) {
			http.NotFound(w, r)
			return
		}
		if parts[1] == "authorize" {
			authorization, err := s.transferAuth.Begin(r.Context(), parts[0])
			if err != nil {
				fail(w, 409, err)
				return
			}
			writeJSON(w, 202, authorization)
			return
		}
		config, err := s.transfers.Configs().Load(parts[0])
		if err != nil {
			fail(w, 404, errors.New("destination unavailable"))
			return
		}
		if config.Kind != "usb" || parts[1] != "remove" {
			if !s.canConfigureTransfers(w, r) {
				return
			}
		}
		if s.transfers.DestinationBusy(parts[0]) {
			fail(w, 409, errors.New("finish or cancel this destination's transfer first"))
			return
		}
		switch parts[1] {
		case "remove":
			if err := s.transfers.Configs().Remove(parts[0]); err != nil {
				fail(w, 400, errors.New("destination could not be removed"))
				return
			}
		case "disconnect":
			config, err := s.transfers.Configs().Load(parts[0])
			if err != nil {
				fail(w, 400, errors.New("destination unavailable"))
				return
			}
			config.Secret.AccessToken, config.Secret.RefreshToken, config.Secret.ExpiresAt = "", "", 0
			s.transferAuth.Cancel(config.ID)
			if err = s.transfers.Configs().Save(config); err != nil {
				fail(w, 400, errors.New("authorization could not be removed"))
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		writeJSON(w, 200, map[string]bool{"saved": true})
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/transfers/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	if err := s.transfers.Action(parts[0], parts[1]); err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"accepted": true})
}

func (s *Server) newTransferStore(ctx context.Context, config archive.Config, source archive.Source) (archive.Store, error) {
	switch config.Kind {
	case "s3":
		return archive.NewS3Store(config, source)
	case "gdrive", "onedrive":
		configs, err := archive.OpenConfigs(filepath.Join(s.transferHome, "destinations"))
		if err != nil {
			return nil, err
		}
		return archive.NewDriveStore(ctx, config, source, configs)
	case "usb":
		root := ""
		var err error
		if s.transferLocalRoot != "" {
			root = s.transferLocalRoot
		} else {
			root, err = s.usbClient.Mount(ctx, config.VolumeUUID)
			if err != nil {
				return nil, archive.DestinationError(err.Error())
			}
		}
		return archive.NewLocalStore(root, source, s.transferEpoch)
	default:
		return nil, errors.New("unsupported transfer destination")
	}
}
