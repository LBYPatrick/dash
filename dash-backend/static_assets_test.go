package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetainedAssetsKeepOpenTabsWorkingAcrossDeployments(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "releases", "v0.1.0-dev.2", "dist")
	old := filepath.Join(root, "releases", "v0.1.0-dev.1", "dist", "assets")
	os.MkdirAll(app, 0700)
	os.MkdirAll(old, 0700)
	os.WriteFile(filepath.Join(app, "index.html"), []byte("new app"), 0600)
	os.WriteFile(filepath.Join(old, "index-BJgJYJEh.css"), []byte("body{color:blue}"), 0600)
	os.WriteFile(filepath.Join(root, "secret.txt"), []byte("private"), 0600)
	server := &Server{app: app, updates: &UpdateManager{home: root}}
	get := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		return response
	}
	response := get("/assets/index-BJgJYJEh.css")
	if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/css") || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") || !strings.Contains(response.Body.String(), "color:blue") {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	response = get("/")
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("app shell must revalidate", response.Code, response.Header())
	}
	for _, path := range []string{"/secret.txt", "/assets/../../secret.txt", "/assets/missing-12345678.css"} {
		if response := get(path); response.Code != 404 {
			t.Fatal("unexpected asset fallback", path, response.Code)
		}
	}
}
