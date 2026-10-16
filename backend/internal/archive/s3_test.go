package archive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestS3MultipartResumesAndNeverOverwrites(t *testing.T) {
	root, _, _ := transferFixture(t)
	data := make([]byte, ChunkBytes+123)
	for i := range data {
		data[i] = byte(i % 251)
	}
	writeOriginal(t, root, "qcamera.ts", data)
	directory, _ := OpenDir(root)
	defer directory.Close()
	source := directorySource{directory}
	file, _ := source.Open(testDrive + "--0/qcamera.ts")
	hash, one, _ := Hash(context.Background(), file)
	modified := file.Modified.UnixNano()
	file.Close()
	a := Asset{Path: testDrive + "--0/qcamera.ts", Key: "dash-archive/v1/test/originals/qcamera.ts", Size: int64(len(data)), Modified: modified, SHA256: hash, SHA1: one}
	var mu sync.Mutex
	parts := map[string]int64{}
	completed := false
	uploads := 0
	headHash := hash
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !strings.Contains(r.Header.Get("Authorization"), "Credential=dummy-access/") {
			t.Error("request was not signed")
		}
		if !strings.HasPrefix(r.URL.Path, "/bucket/prefix/") {
			t.Error("custom endpoint/path-style/prefix not applied")
		}
		query := r.URL.Query()
		switch {
		case r.Method == "HEAD":
			if !completed {
				w.Header().Set("X-Amz-Error-Code", "NotFound")
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(a.Size))
			w.Header().Set("X-Amz-Meta-Dash-Sha256", headHash)
		case r.Method == "POST" && query.Has("uploads"):
			uploads++
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>private-upload-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT" && query.Get("uploadId") != "":
			number := query.Get("partNumber")
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				t.Error(err)
			}
			parts[number] = n
			w.Header().Set("ETag", `"part-`+number+`"`)
		case r.Method == "POST" && query.Get("uploadId") != "":
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("conditional completion missing")
			}
			completed = true
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"not-a-file-hash"</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	config := Config{ID: "s3", Name: "MinIO", Kind: "s3", Bucket: "bucket", Region: "us-east-1", Prefix: "prefix", Endpoint: server.URL, PathStyle: true, AllowInsecureHTTP: true, Secret: Secret{AccessKey: "dummy-access", SecretKey: "dummy-secret"}}
	for i := 0; i < 8; i++ {
		store, err := NewS3Store(config, source)
		if err != nil {
			t.Fatal(err)
		}
		cp, done, err := store.Step(context.Background(), a)
		store.Close()
		if err != nil {
			t.Fatal(err)
		}
		a.Checkpoint = cp
		if done {
			break
		}
		if i == 7 {
			t.Fatal("multipart did not complete")
		}
	}
	if uploads != 1 || parts["1"] != ChunkBytes || parts["2"] != 123 {
		t.Fatal("part checkpoints were not reused", uploads, parts)
	}
	store, _ := NewS3Store(config, source)
	defer store.Close()
	a.Checkpoint = Checkpoint{}
	_, done, err := store.Step(context.Background(), a)
	if err != nil || !done || uploads != 1 {
		t.Fatal("existing verified object was uploaded again", err)
	}
	headHash = "different"
	if _, _, err = store.Step(context.Background(), a); err != ErrConflict {
		t.Fatal("foreign object would be overwritten", err)
	}
}
func TestSourceConfigAndPrivateErrors(t *testing.T) {
	config := Config{ID: "s3", Name: "Storage", Kind: "s3", Bucket: "b", Region: "r", Endpoint: "http://example.invalid", Secret: Secret{AccessKey: "dummy", SecretKey: "dummy"}}
	if config.Validate() == nil {
		t.Fatal("plaintext endpoint was silently accepted")
	}
	err := remoteError("S3", "upload", fmt.Errorf("https://private-upload.invalid?token=private-marker"))
	if strings.Contains(err.Error(), "private-marker") {
		t.Fatal("private URL escaped")
	}
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "private.json"), []byte(`{}`), 0600)
	configs, _ := OpenConfigs(root)
	os.Symlink(filepath.Join(outside, "private.json"), filepath.Join(root, "s3.json"))
	if _, err := configs.Load("s3"); err == nil {
		t.Fatal("configuration followed a symlink")
	}
}
