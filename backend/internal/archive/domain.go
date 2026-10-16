// Package archive owns durable, originals-only batch transfers. It does not
// depend on the disposable recording index or the browser download job queue.
package archive

import (
	"context"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const ChunkBytes int64 = 10 << 20 // S3 >=5 MiB; Google 256 KiB; OneDrive 320 KiB.

var (
	ErrConflict     = errors.New("destination has a different file; existing data was preserved")
	ErrMissing      = errors.New("source recording is missing or changed")
	ErrDisconnected = errors.New("destination disconnected; reconnect it and resume")
	ErrReconnect    = errors.New("provider authorization expired; reconnect this destination")
	ErrFull         = errors.New("USB drive has insufficient free space; free space or select another drive")
	IDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	SessionPattern  = regexp.MustCompile(`^([a-zA-Z0-9_-]+--[a-zA-Z0-9_-]+)--([0-9]+)$`)
)

type DestinationError string

func (e DestinationError) Error() string { return string(e) }

// Secret is deliberately never part of a public destination or transfer view.
type Secret struct {
	AccessKey    string `json:"accessKey,omitempty"`
	SecretKey    string `json:"secretKey,omitempty"`
	SessionToken string `json:"sessionToken,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

// Config is a private, owner-readable file configured over SSH or trusted TLS.
type Config struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Endpoint          string `json:"endpoint,omitempty"`
	Region            string `json:"region,omitempty"`
	Bucket            string `json:"bucket,omitempty"`
	Prefix            string `json:"prefix,omitempty"`
	PathStyle         bool   `json:"pathStyle,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHttp,omitempty"`
	VolumeUUID        string `json:"volumeUuid,omitempty"`
	ClientID          string `json:"clientId,omitempty"`
	Tenant            string `json:"tenant,omitempty"`
	FolderID          string `json:"folderId,omitempty"`
	Secret            Secret `json:"secret"`
}

type Destination struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Location      string `json:"location"`
	Connected     bool   `json:"connected"`
	Authorization string `json:"authorization,omitempty"`
}

func (c Config) Public() Destination {
	location := c.Bucket
	connected := c.Secret.AccessKey != "" && c.Secret.SecretKey != ""
	if c.Kind == "usb" {
		location, connected = c.VolumeUUID, true
	}
	if c.Kind == "gdrive" || c.Kind == "onedrive" {
		location, connected = c.FolderID, c.Secret.RefreshToken != "" || c.Secret.AccessToken != ""
	}
	return Destination{ID: c.ID, Name: c.Name, Kind: c.Kind, Location: location, Connected: connected}
}

func (c Config) Validate() error {
	if !IDPattern.MatchString(c.ID) || len(c.Name) == 0 || len(c.Name) > 80 {
		return errors.New("destination needs a short name and an id containing lowercase letters, digits or hyphens")
	}
	if len(c.Prefix) > 512 || strings.ContainsAny(c.Prefix, "\\\r\n") || strings.HasPrefix(c.Prefix, "/") {
		return errors.New("invalid archive prefix")
	}
	for _, part := range strings.Split(c.Prefix, "/") {
		if part == ".." || part == "." {
			return errors.New("invalid archive prefix")
		}
	}
	switch c.Kind {
	case "s3":
		if c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\\r\n") || c.Region == "" || c.Secret.AccessKey == "" || c.Secret.SecretKey == "" {
			return errors.New("S3 requires bucket, region and private credentials")
		}
		if c.Endpoint != "" {
			u, err := url.Parse(c.Endpoint)
			if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && c.AllowInsecureHTTP)) {
				return errors.New("S3 endpoint must use HTTPS unless insecure HTTP was explicitly configured")
			}
		}
	case "usb":
		if !regexp.MustCompile(`^[A-Za-z0-9-]{4,64}$`).MatchString(c.VolumeUUID) {
			return errors.New("select a USB volume UUID")
		}
	case "gdrive", "onedrive":
		if c.ClientID == "" || len(c.ClientID) > 256 || len(c.Tenant) > 128 || strings.ContainsAny(c.Tenant, "/\\?#\r\n") {
			return errors.New("provider application client id is required")
		}
	default:
		return errors.New("unsupported destination provider")
	}
	return nil
}

type Part struct {
	Number int32  `json:"number"`
	ETag   string `json:"etag"`
}

// Checkpoint contains private resumable upload capabilities. Never return it in
// a public API, diagnostic dump, log line or exported manifest.
type Checkpoint struct {
	VerifiedEpoch string `json:"verifiedEpoch,omitempty"`
	Offset        int64  `json:"offset"`
	UploadID      string `json:"uploadId,omitempty"`
	UploadURL     string `json:"uploadUrl,omitempty"`
	Parts         []Part `json:"parts,omitempty"`
	RemoteID      string `json:"remoteId,omitempty"`
	ExpiresAt     int64  `json:"expiresAt,omitempty"`
}
type Asset struct {
	Path       string
	Key        string
	Session    string
	Size       int64
	Modified   int64
	SHA256     string
	SHA1       string
	Status     string
	Checkpoint Checkpoint
}
type Batch struct {
	ID               string `json:"id"`
	DestinationID    string `json:"destinationId"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	Created          int64  `json:"created"`
	TotalFiles       int64  `json:"totalFiles"`
	TotalBytes       int64  `json:"totalBytes"`
	CompletedFiles   int64  `json:"completedFiles"`
	CompletedBytes   int64  `json:"completedBytes"`
	TransferredBytes int64  `json:"transferredBytes"`
	Issues           int64  `json:"issues"`
	CurrentFile      string `json:"currentFile,omitempty"`
	AutoResume       bool   `json:"autoResume"`
	CleanupPending   bool   `json:"cleanupPending"`
}
type Source interface {
	Open(string) (*SourceFile, error)
}
type SourceFile struct {
	io.ReadSeekCloser
	ReaderAt io.ReaderAt
	Size     int64
	Modified time.Time
}
type Store interface {
	Step(context.Context, Asset) (Checkpoint, bool, error)
	Put(context.Context, string, []byte) error
	Abort(context.Context, Asset) error
	Close() error
}
type StoreFactory func(context.Context, Config, Source) (Store, error)
type Options struct {
	Root    string
	Home    string
	State   func() string
	Acquire func(context.Context) (func(), error)
	Factory StoreFactory
}

func terminal(status string) bool {
	return status == "complete" || status == "complete_with_issues" || status == "canceled"
}
