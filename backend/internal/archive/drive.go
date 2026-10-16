package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type folderCache struct {
	sync.Mutex
	ids map[string]string
}

var driveFolders = folderCache{ids: map[string]string{}}

type DriveStore struct {
	config      Config
	source      Source
	token       string
	client      *http.Client
	api, upload string
	folders     *folderCache
}
type driveItem struct {
	ID            string            `json:"id"`
	MimeType      string            `json:"mimeType"`
	Folder        *struct{}         `json:"folder"`
	Size          json.Number       `json:"size"`
	AppProperties map[string]string `json:"appProperties"`
	File          struct {
		Hashes struct {
			SHA1   string `json:"sha1Hash"`
			SHA256 string `json:"sha256Hash"`
		} `json:"hashes"`
	} `json:"file"`
}

func NewDriveStore(ctx context.Context, c Config, source Source, configs *Configs) (*DriveStore, error) {
	client := providerHTTP()
	token, err := accessToken(ctx, c, configs, client, oauthEndpoints(c))
	if err != nil {
		return nil, err
	}
	api, upload := "https://www.googleapis.com/drive/v3", "https://www.googleapis.com/upload/drive/v3"
	if c.Kind == "onedrive" {
		api = "https://graph.microsoft.com/v1.0"
		upload = api
	}
	return &DriveStore{config: c, source: source, token: token, client: client, api: api, upload: upload, folders: &driveFolders}, nil
}
func (s *DriveStore) Close() error { return nil }
func (s *DriveStore) request(ctx context.Context, method, address string, body io.Reader, headers map[string]string, authorized bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, errors.New("invalid provider request")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if authorized {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	if v := request.Header.Get("Content-Length"); v != "" {
		request.ContentLength, _ = strconv.ParseInt(v, 10, 64)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, RemoteError{s.config.Kind, "connect", 0}
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		response.Body.Close()
		return nil, ErrReconnect
	}
	return response, nil
}
func decodeProvider(response *http.Response, target any, provider, operation string) error {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RemoteError{provider, operation, response.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
		return RemoteError{provider, "decode response", response.StatusCode}
	}
	return nil
}
func (s *DriveStore) jsonRequest(ctx context.Context, method, address string, body any, target any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, err := s.request(ctx, method, address, bytes.NewReader(data), map[string]string{"Content-Type": "application/json"}, true)
	if err != nil {
		return err
	}
	return decodeProvider(response, target, s.config.Kind, "request")
}
func (s *DriveStore) find(ctx context.Context, parent, name string) (driveItem, bool, error) {
	var item driveItem
	if s.config.Kind == "gdrive" {
		quote := func(value string) string {
			return strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'")
		}
		query := url.Values{"q": {"'" + quote(parent) + "' in parents and name = '" + quote(name) + "' and trashed = false"}, "fields": {"files(id,size,appProperties,mimeType),nextPageToken"}, "pageSize": {"2"}}
		response, err := s.request(ctx, "GET", s.api+"/files?"+query.Encode(), nil, nil, true)
		if err != nil {
			return item, false, err
		}
		var listing struct {
			Files []driveItem `json:"files"`
			Next  string      `json:"nextPageToken"`
		}
		if err = decodeProvider(response, &listing, s.config.Kind, "list files"); err != nil {
			return item, false, err
		}
		if len(listing.Files) > 1 || listing.Next != "" {
			return item, false, ErrConflict
		}
		if len(listing.Files) == 0 {
			return item, false, nil
		}
		return listing.Files[0], true, nil
	}
	response, err := s.request(ctx, "GET", s.api+"/me/drive/items/"+url.PathEscape(parent)+":/"+url.PathEscape(name)+"?$select=id,size,file,folder", nil, nil, true)
	if err != nil {
		return item, false, err
	}
	if response.StatusCode == 404 {
		response.Body.Close()
		return item, false, nil
	}
	if err = decodeProvider(response, &item, s.config.Kind, "inspect file"); err != nil {
		return item, false, err
	}
	return item, true, nil
}
func (s *DriveStore) parent(ctx context.Context, key string) (string, string, error) {
	key = path.Join(s.config.Prefix, key)
	if !validRelative(key) {
		return "", "", errors.New("invalid provider path")
	}
	directory, name := path.Split(key)
	parent := s.config.FolderID
	if parent == "" {
		parent = "root"
	}
	if s.config.Kind == "onedrive" && parent == "root" {
		var item driveItem
		response, err := s.request(ctx, "GET", s.api+"/me/drive/root?$select=id", nil, nil, true)
		if err != nil {
			return "", "", err
		}
		if err = decodeProvider(response, &item, s.config.Kind, "open root"); err != nil {
			return "", "", err
		}
		parent = item.ID
	}
	for _, component := range strings.Split(strings.TrimSuffix(directory, "/"), "/") {
		if component == "" {
			continue
		}
		cacheKey := fingerprint(s.config) + ":" + parent + ":" + component
		s.folders.Lock()
		id := s.folders.ids[cacheKey]
		s.folders.Unlock()
		if id != "" {
			parent = id
			continue
		}
		item, found, err := s.find(ctx, parent, component)
		if err != nil {
			return "", "", err
		}
		if found && ((s.config.Kind == "gdrive" && item.MimeType != "application/vnd.google-apps.folder") || (s.config.Kind == "onedrive" && item.Folder == nil)) {
			return "", "", ErrConflict
		}
		if !found {
			if s.config.Kind == "gdrive" {
				err = s.jsonRequest(ctx, "POST", s.api+"/files?fields=id", map[string]any{"name": component, "parents": []string{parent}, "mimeType": "application/vnd.google-apps.folder"}, &item)
			} else {
				err = s.jsonRequest(ctx, "POST", s.api+"/me/drive/items/"+url.PathEscape(parent)+"/children", map[string]any{"name": component, "folder": map[string]any{}, "@microsoft.graph.conflictBehavior": "fail"}, &item)
			}
			if err != nil {
				return "", "", err
			}
		}
		if item.ID == "" {
			return "", "", errors.New("provider returned an empty folder id")
		}
		s.folders.Lock()
		s.folders.ids[cacheKey] = item.ID
		s.folders.Unlock()
		parent = item.ID
	}
	return parent, name, nil
}
func (s *DriveStore) verify(ctx context.Context, a Asset, item driveItem) error {
	size, err := item.Size.Int64()
	if err != nil || size != a.Size {
		return ErrConflict
	}
	if s.config.Kind == "gdrive" {
		if item.AppProperties["dash_sha256"] != a.SHA256 {
			return ErrConflict
		}
		return nil
	}
	if item.File.Hashes.SHA256 != "" {
		if !strings.EqualFold(item.File.Hashes.SHA256, a.SHA256) {
			return ErrConflict
		}
		return nil
	}
	if item.File.Hashes.SHA1 != "" && a.SHA1 != "" {
		if !strings.EqualFold(item.File.Hashes.SHA1, a.SHA1) {
			return ErrConflict
		}
		return nil
	}
	response, err := s.request(ctx, "GET", s.api+"/me/drive/items/"+url.PathEscape(item.ID)+"/content", nil, nil, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return RemoteError{s.config.Kind, "verify uploaded file", response.StatusCode}
	}
	hash, _, err := Hash(ctx, io.LimitReader(response.Body, a.Size+1))
	if err != nil {
		return err
	}
	if hash != a.SHA256 {
		return ErrConflict
	}
	return nil
}
func (s *DriveStore) validUpload(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if s.config.Kind == "gdrive" {
		return host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
	}
	return strings.HasSuffix(host, ".1drv.com") || strings.HasSuffix(host, ".sharepoint.com") || strings.HasSuffix(host, ".onedrive.com") || strings.HasSuffix(host, ".microsoftpersonalcontent.com")
}
func (s *DriveStore) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	cp := a.Checkpoint
	parent, name, err := s.parent(ctx, a.Key)
	if err != nil {
		return cp, false, err
	}
	if cp.UploadURL == "" {
		item, found, err := s.find(ctx, parent, name)
		if err != nil {
			return cp, false, err
		}
		if found {
			if err = s.verify(ctx, a, item); err != nil {
				return cp, false, err
			}
			return Checkpoint{Offset: a.Size}, true, nil
		}
		if s.config.Kind == "onedrive" && a.Size == 0 {
			response, err := s.request(ctx, "PUT", s.api+"/me/drive/items/"+url.PathEscape(parent)+":/"+url.PathEscape(name)+":/content?%40microsoft.graph.conflictBehavior=fail", bytes.NewReader(nil), map[string]string{"If-None-Match": "*", "Content-Length": "0", "Content-Type": "application/octet-stream"}, true)
			if err != nil {
				return cp, false, err
			}
			defer response.Body.Close()
			if response.StatusCode != 200 && response.StatusCode != 201 {
				return cp, false, RemoteError{s.config.Kind, "create empty file", response.StatusCode}
			}
			return cp, true, nil
		}
		if s.config.Kind == "gdrive" {
			if a.Size == 0 {
				var item driveItem
				err = s.jsonRequest(ctx, "POST", s.api+"/files?fields=id,size,appProperties", map[string]any{"name": name, "parents": []string{parent}, "mimeType": "application/octet-stream", "appProperties": map[string]string{"dash_sha256": a.SHA256}}, &item)
				if err != nil {
					return cp, false, err
				}
				if err = s.verify(ctx, a, item); err != nil {
					return cp, false, err
				}
				return Checkpoint{}, true, nil
			}
			data, _ := json.Marshal(map[string]any{"name": name, "parents": []string{parent}, "appProperties": map[string]string{"dash_sha256": a.SHA256}})
			response, err := s.request(ctx, "POST", s.upload+"/files?uploadType=resumable", bytes.NewReader(data), map[string]string{"Content-Type": "application/json", "X-Upload-Content-Type": "application/octet-stream", "X-Upload-Content-Length": strconv.FormatInt(a.Size, 10)}, true)
			if err != nil {
				return cp, false, err
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				return cp, false, RemoteError{s.config.Kind, "start upload", response.StatusCode}
			}
			cp.UploadURL = response.Header.Get("Location")
		} else {
			var result struct {
				UploadURL string `json:"uploadUrl"`
			}
			err = s.jsonRequest(ctx, "POST", s.api+"/me/drive/items/"+url.PathEscape(parent)+":/"+url.PathEscape(name)+":/createUploadSession", map[string]any{"item": map[string]any{"name": name, "@microsoft.graph.conflictBehavior": "fail"}}, &result)
			if err != nil {
				return cp, false, err
			}
			cp.UploadURL = result.UploadURL
		}
		if !s.validUpload(cp.UploadURL) {
			return a.Checkpoint, false, errors.New("provider returned an untrusted upload endpoint")
		}
		return cp, false, nil
	}
	if !s.validUpload(cp.UploadURL) {
		return cp, false, errors.New("saved provider upload endpoint is invalid")
	}
	// Ask the server for its acknowledged range. This repairs lost responses,
	// including a power cut between a successful chunk and the SQLite commit.
	method := "PUT"
	headers := map[string]string{"Content-Length": "0", "Content-Range": fmt.Sprintf("bytes */%d", a.Size)}
	authorized := true
	if s.config.Kind == "onedrive" {
		method = "GET"
		headers = nil
		authorized = false
	}
	response, err := s.request(ctx, method, cp.UploadURL, bytes.NewReader(nil), headers, authorized)
	if err != nil {
		return cp, false, err
	}
	if response.StatusCode == 404 || response.StatusCode == 410 {
		response.Body.Close()
		return Checkpoint{}, false, nil
	}
	if s.config.Kind == "gdrive" {
		if response.StatusCode == 200 || response.StatusCode == 201 {
			response.Body.Close()
			// Verify final file metadata through the normal lookup on the next
			// step, including a final response lost before checkpointing.
			return Checkpoint{Offset: a.Size}, false, nil
		}
		if response.StatusCode != 308 {
			response.Body.Close()
			return cp, false, RemoteError{s.config.Kind, "resume upload", response.StatusCode}
		}
		cp.Offset = 0
		if r := response.Header.Get("Range"); strings.HasPrefix(r, "bytes=0-") {
			last, e := strconv.ParseInt(strings.TrimPrefix(r, "bytes=0-"), 10, 64)
			if e != nil {
				response.Body.Close()
				return cp, false, errors.New("invalid provider upload range")
			}
			cp.Offset = last + 1
		}
		response.Body.Close()
	} else {
		var state struct {
			Ranges []string `json:"nextExpectedRanges"`
		}
		if err = decodeProvider(response, &state, s.config.Kind, "resume upload"); err != nil {
			return cp, false, err
		}
		if len(state.Ranges) != 1 {
			return cp, false, errors.New("provider upload range requires reconnection")
		}
		first, _, _ := strings.Cut(state.Ranges[0], "-")
		cp.Offset, err = strconv.ParseInt(first, 10, 64)
		if err != nil {
			return cp, false, errors.New("invalid provider upload range")
		}
	}
	if cp.Offset < 0 || cp.Offset >= a.Size {
		return cp, false, errors.New("provider upload offset is outside the source")
	}
	source, err := s.source.Open(a.Path)
	if err != nil {
		return cp, false, ErrMissing
	}
	defer source.Close()
	if !unchanged(source, a) {
		return cp, false, ErrMissing
	}
	count := min(ChunkBytes, a.Size-cp.Offset)
	headers = map[string]string{"Content-Length": strconv.FormatInt(count, 10), "Content-Type": "application/octet-stream", "Content-Range": fmt.Sprintf("bytes %d-%d/%d", cp.Offset, cp.Offset+count-1, a.Size)}
	response, err = s.request(ctx, "PUT", cp.UploadURL, io.NewSectionReader(source.ReaderAt, cp.Offset, count), headers, s.config.Kind == "gdrive")
	if err != nil {
		return cp, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == 200 || response.StatusCode == 201 {
		return Checkpoint{Offset: a.Size}, false, nil
	}
	if (s.config.Kind == "gdrive" && response.StatusCode != 308) || (s.config.Kind == "onedrive" && response.StatusCode != 202) {
		return cp, false, RemoteError{s.config.Kind, "upload chunk", response.StatusCode}
	}
	cp.Offset += count
	return cp, false, nil
}

type memorySource struct{ data []byte }
type memoryFile struct{ *bytes.Reader }

func (f memoryFile) Close() error { return nil }
func (s memorySource) Open(string) (*SourceFile, error) {
	reader := bytes.NewReader(s.data)
	return &SourceFile{ReadSeekCloser: memoryFile{reader}, ReaderAt: reader, Size: int64(len(s.data))}, nil
}
func (s *DriveStore) Put(ctx context.Context, key string, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	sum := sha256.Sum256(data)
	oneSource := memorySource{data}
	copyStore := *s
	copyStore.source = oneSource
	a := Asset{Path: "manifest", Key: key, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
	a.Modified = oneSourceModified()
	for attempt := 0; attempt < 32; attempt++ {
		cp, done, err := copyStore.Step(ctx, a)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		a.Checkpoint = cp
	}
	return errors.New("provider repeatedly interrupted manifest publication")
}
func oneSourceModified() int64 { return (SourceFile{}).Modified.UnixNano() }
func (s *DriveStore) Abort(ctx context.Context, a Asset) error {
	if a.Checkpoint.UploadURL == "" {
		return nil
	}
	if !s.validUpload(a.Checkpoint.UploadURL) {
		return errors.New("invalid saved upload endpoint")
	}
	response, err := s.request(ctx, "DELETE", a.Checkpoint.UploadURL, nil, nil, s.config.Kind == "gdrive")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 200 || response.StatusCode == 204 || response.StatusCode == 404 || response.StatusCode == 410 {
		return nil
	}
	return RemoteError{s.config.Kind, "cancel upload", response.StatusCode}
}
