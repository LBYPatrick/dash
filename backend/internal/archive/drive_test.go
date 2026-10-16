package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(status int, value any) *http.Response {
	data, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}
}
func TestDriveResumesAcknowledgedChunksAndVerifies(t *testing.T) {
	for _, kind := range []string{"gdrive", "onedrive"} {
		t.Run(kind, func(t *testing.T) {
			data := bytes.Repeat([]byte("x"), int(ChunkBytes)+17)
			sum := sha256.Sum256(data)
			a := Asset{Path: "file", Key: "footage.hevc", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Modified: oneSourceModified()}
			var offset int64
			var stored bool
			var chunks int
			var queried int
			session := "https://upload.googleapis.com/session"
			if kind == "onedrive" {
				session = "https://upload.1drv.com/session"
			}
			store := &DriveStore{config: Config{ID: "test", Kind: kind, FolderID: "parent"}, source: memorySource{data}, token: "private-access-token", api: "https://provider.test", upload: "https://provider.test/upload", folders: &folderCache{ids: map[string]string{}}}
			store.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == session {
					if kind == "onedrive" && r.Header.Get("Authorization") != "" {
						t.Error("bearer token sent to preauthorized upload URL")
					}
					if r.Method == "GET" || strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */") {
						queried++
						if stored {
							return reply(404, nil), nil
						}
						if kind == "gdrive" {
							res := reply(308, nil)
							if offset > 0 {
								res.Header.Set("Range", fmt.Sprintf("bytes=0-%d", offset-1))
							}
							return res, nil
						}
						return reply(200, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", offset)}}), nil
					}
					b, e := io.ReadAll(r.Body)
					if e != nil {
						t.Fatal(e)
					}
					expected := fmt.Sprintf("bytes %d-%d/%d", offset, offset+int64(len(b))-1, a.Size)
					if r.Header.Get("Content-Range") != expected || !bytes.Equal(b, data[offset:offset+int64(len(b))]) {
						t.Fatalf("bad chunk range %s", r.Header.Get("Content-Range"))
					}
					chunks++
					offset += int64(len(b))
					if offset == a.Size {
						stored = true
						return reply(201, map[string]any{"id": "file"}), nil
					}
					if kind == "gdrive" {
						return reply(308, nil), nil
					}
					return reply(202, map[string]any{"nextExpectedRanges": []string{fmt.Sprintf("%d-", offset)}}), nil
				}
				if r.Header.Get("Authorization") != "Bearer private-access-token" {
					t.Error("missing API authorization")
				}
				if r.Method == "GET" {
					item := map[string]any{"id": "file", "size": strconv.FormatInt(a.Size, 10), "appProperties": map[string]string{"dash_sha256": a.SHA256}, "file": map[string]any{"hashes": map[string]string{"sha256Hash": a.SHA256}}}
					if kind == "gdrive" {
						files := []any{}
						if stored {
							files = append(files, item)
						}
						return reply(200, map[string]any{"files": files}), nil
					}
					if !stored {
						return reply(404, nil), nil
					}
					item["size"] = a.Size
					return reply(200, item), nil
				}
				if kind == "gdrive" {
					res := reply(200, nil)
					res.Header.Set("Location", session)
					return res, nil
				}
				return reply(200, map[string]string{"uploadUrl": session}), nil
			})}
			// Create the upload session, then emulate an acknowledged first chunk whose
			// local checkpoint was lost in an abrupt power cut.
			cp, done, err := store.Step(context.Background(), a)
			if err != nil || done || cp.UploadURL == "" {
				t.Fatal(cp, done, err)
			}
			offset = ChunkBytes
			a.Checkpoint = cp
			for attempt := 0; attempt < 5; attempt++ {
				cp, done, err = store.Step(context.Background(), a)
				if err != nil {
					t.Fatal(err)
				}
				a.Checkpoint = cp
				if done {
					break
				}
			}
			if !done || !stored || chunks != 1 || queried < 1 {
				t.Fatal(done, stored, chunks, queried)
			}
		})
	}
}
func TestGoogleEmptyFileAndFolderConflict(t *testing.T) {
	sum := sha256.Sum256(nil)
	a := Asset{Path: "file", Key: "empty", SHA256: hex.EncodeToString(sum[:])}
	store := &DriveStore{config: Config{Kind: "gdrive"}, api: "https://provider.test", folders: &folderCache{ids: map[string]string{}}}
	store.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			return reply(200, map[string]any{"files": []any{}}), nil
		}
		return reply(200, map[string]any{"id": "empty", "size": "0", "appProperties": map[string]string{"dash_sha256": a.SHA256}}), nil
	})}
	if _, done, err := store.Step(context.Background(), a); err != nil || !done {
		t.Fatal(done, err)
	}
	a.Key = "folder/empty"
	store.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return reply(200, map[string]any{"files": []any{map[string]any{"id": "existing-file", "size": "0"}}}), nil
	})
	if _, _, err := store.Step(context.Background(), a); err != ErrConflict {
		t.Fatal(err)
	}
}
func TestProviderUploadTrustAndErrorRedaction(t *testing.T) {
	for _, kind := range []string{"gdrive", "onedrive"} {
		s := &DriveStore{config: Config{Kind: kind}}
		for _, u := range []string{"http://upload.googleapis.com/", "https://evil.example/?private=capability", "https://googleapis.com.evil.example/", "https://user:secret@upload.1drv.com/"} {
			if s.validUpload(u) {
				t.Fatal("unsafe upload URL")
			}
		}
	}
}
func TestBundledRegistrationUsesApplicationNotAccountTokens(t *testing.T) {
	old := BundledRegistration
	defer func() { BundledRegistration = old }()
	data, _ := json.Marshal(registration{GoogleClientID: "test-app", GoogleClientSecret: "test-app-secret", MicrosoftClientID: "test-ms"})
	BundledRegistration = base64.RawStdEncoding.EncodeToString(data)
	configs, err := OpenConfigs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = configs.Save(Config{ID: "google", Name: "Google", Kind: "gdrive"}); err != nil {
		t.Fatal(err)
	}
	c, err := configs.Load("google")
	if err != nil || c.ClientID != "test-app" || c.Secret.ClientSecret != "test-app-secret" || c.Public().Connected {
		t.Fatal(c.Public(), err)
	}
	public, _ := json.Marshal(BundledProviders())
	if strings.Contains(string(public), "test-app") {
		t.Fatal("registration leaked")
	}
}
func TestOAuthRefreshRotatesTokensPrivately(t *testing.T) {
	configs, err := OpenConfigs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Config{ID: "google", Name: "Google", Kind: "gdrive", ClientID: "application", Secret: Secret{RefreshToken: "old-private-refresh"}}
	if err = configs.Save(c); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-private-refresh" {
			t.Error("invalid refresh request")
		}
		return reply(200, map[string]any{"access_token": "new-private-access", "refresh_token": "new-private-refresh", "expires_in": 3600}), nil
	})}
	token, err := accessToken(context.Background(), c, configs, client, OAuthEndpoints{Token: "https://provider.test/token"})
	if err != nil || token != "new-private-access" {
		t.Fatal(err)
	}
	saved, _ := configs.Load(c.ID)
	if saved.Secret.RefreshToken != "new-private-refresh" {
		t.Fatal("refresh rotation lost")
	}
	public, _ := json.Marshal(saved.Public())
	if strings.Contains(string(public), "private") {
		t.Fatal("token exposed")
	}
}
func TestParkedRequestStopsInflightAuthorization(t *testing.T) {
	var parked atomic.Bool
	parked.Store(true)
	state := func() string {
		if parked.Load() {
			return "parked"
		}
		return "unknown"
	}
	ctx, cancel := parkedRequest(context.Background(), state)
	defer cancel()
	parked.Store(false)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("authorization continued without parked state")
	}
}

func TestDeviceCodeAuthorizationStoresTokensAfterConsent(t *testing.T) {
	for _, kind := range []string{"gdrive", "onedrive"} {
		t.Run(kind, func(t *testing.T) {
			configs, err := OpenConfigs(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			c := Config{ID: "provider", Name: "Archive", Kind: kind, ClientID: "test-registered-application"}
			if kind == "gdrive" {
				c.Secret.ClientSecret = "test-application-secret"
			}
			if err = configs.Save(c); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			auth := NewAuthorizer(ctx, configs, func() string { return "parked" })
			auth.endpoints = func(Config) OAuthEndpoints {
				return OAuthEndpoints{Device: "https://provider.test/device", Token: "https://provider.test/token"}
			}
			auth.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("client_id") != c.ClientID {
					t.Error("wrong OAuth application")
				}
				if r.URL.Path == "/device" {
					scope := "https://www.googleapis.com/auth/drive.file"
					if kind == "onedrive" {
						scope = "offline_access Files.ReadWrite"
					}
					if r.Form.Get("scope") != scope {
						t.Error("requested excessive scope")
					}
					return reply(200, map[string]any{"device_code": "private-device-capability", "user_code": "PUBLIC-CODE", "verification_uri": "https://provider.test/activate", "expires_in": 600, "interval": 1}), nil
				}
				if r.Form.Get("device_code") != "private-device-capability" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("client_secret") != c.Secret.ClientSecret {
					t.Error("invalid device token exchange")
				}
				return reply(200, map[string]any{"access_token": "private-account-access", "refresh_token": "private-account-refresh", "expires_in": 3600}), nil
			})}
			value, err := auth.Begin(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(value)
			if bytes.Contains(data, []byte("private")) {
				t.Fatal("device capability leaked")
			}
			for {
				snap := auth.Snapshot()
				if len(snap) > 0 && snap[0].Status == "connected" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("device authorization did not finish")
				case <-time.After(20 * time.Millisecond):
				}
			}
			saved, err := configs.Load(c.ID)
			if err != nil || saved.Secret.RefreshToken != "private-account-refresh" {
				t.Fatal("tokens not stored privately", err)
			}
		})
	}
}
