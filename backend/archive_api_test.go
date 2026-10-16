package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"dash.local/backend/internal/archive"
)

func TestTransferPrivateSetupAndPublicRedaction(t *testing.T) {
	index, _ := fixtureIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exports, err := newExports(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	app := newServer(ctx, index, exports, t.TempDir(), t.TempDir(), t.TempDir(), "")
	app.transferHome = t.TempDir()
	app.transferLocalRoot = t.TempDir()
	app.transferEpoch = "test"
	manager, err := archive.New(ctx, archive.Options{Root: index.root, Home: app.transferHome, State: func() string { return "unknown" }, Acquire: acquireCompute, Factory: app.newTransferStore})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	app.transfers = manager
	app.transferAuth = archive.NewAuthorizer(ctx, manager.Configs(), func() string { return "unknown" })
	key, err := transferSetupKey(app.transferHome)
	if err != nil {
		t.Fatal(err)
	}
	private := `{"id":"s3","name":"Backup","kind":"s3","bucket":"test-bucket","region":"test-region","secret":{"accessKey":"private-access-key","secretKey":"private-secret-key"}}`
	request := func(path, body, remote, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if body != "" {
			r = httptest.NewRequest("POST", path, strings.NewReader(body))
		}
		r.RemoteAddr = remote
		if key != "" {
			r.Header.Set("Authorization", "DashSetup "+key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if w := request("/api/transfer-destinations", private, "192.168.1.2:1234", key); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("/api/transfer-destinations", private, "127.0.0.1:1234", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("/api/transfer-destinations", private, "127.0.0.1:1234", key); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("/api/transfers", "", "192.168.1.2:1234", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), key) {
		t.Fatal("private credentials leaked")
	}
	var snapshot map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["configurationSecure"] != false {
		t.Fatal("LAN HTTP advertised credential entry")
	}
	usb := `{"id":"s3","name":"Overwrite","kind":"usb","volumeUuid":"DASH-TEST-0001","secret":{}}`
	if w = request("/api/transfer-destinations", usb, "192.168.1.2:1234", ""); w.Code != 403 {
		t.Fatal("USB could overwrite cloud configuration", w.Code)
	}
	usb = strings.Replace(usb, `"id":"s3"`, `"id":"usb"`, 1)
	if w = request("/api/transfer-destinations", usb, "192.168.1.2:1234", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("/api/transfer-destinations/usb/remove", "{}", "192.168.1.2:1234", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	info, err := os.Stat(app.transferHome + "/setup.key")
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("setup key permissions", err)
	}
}
