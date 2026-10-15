package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Set by CI; source builds are deliberately not OTA-managed releases.
var buildVersion = "0.1.0-local"
var buildChannel = "local"
var buildRevision = "unknown"
var buildSequence = "0"

const releaseProtocol = 1
const maxReleaseArchive int64 = 128 << 20
const maxReleaseExpanded int64 = 256 << 20

var releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

type Build struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Channel  string `json:"channel"`
	Revision string `json:"revision"`
	Sequence uint64 `json:"sequence"`
	Tag      string `json:"tag"`
	Arch     string `json:"arch"`
}
type ReleaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type ReleaseManifest struct {
	Build
	Assets map[string]ReleaseAsset `json:"assets"`
}

func currentBuild() Build {
	sequence, _ := strconv.ParseUint(buildSequence, 10, 64)
	tag := "v" + buildVersion
	return Build{releaseProtocol, buildVersion, buildChannel, buildRevision, sequence, tag, runtime.GOARCH}
}
func validChannel(channel string) bool {
	return channel == "main" || channel == "nightly" || channel == "dev"
}
func (b Build) validate(channel, arch string) error {
	if b.Protocol != releaseProtocol || (!validChannel(b.Channel) && b.Channel != "local") || b.Channel != channel || b.Arch != arch || b.Sequence == 0 || !releaseIDPattern.MatchString(b.Tag) || b.Tag != "v"+b.Version || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(b.Revision) {
		return errors.New("invalid or incompatible release identity")
	}
	// The channel is in every immutable tag, including main, so a feed cannot
	// accidentally install a different channel's archive.
	suffix := "-" + channel + "."
	if channel == "main" {
		suffix = "+main."
	}
	if !strings.Contains(b.Version, suffix+strconv.FormatUint(b.Sequence, 10)+".") {
		return errors.New("release tag does not match its channel/build number")
	}
	return nil
}
func (m ReleaseManifest) asset(channel, arch string) (ReleaseAsset, error) {
	if !validChannel(channel) {
		return ReleaseAsset{}, errors.New("local builds are not release feeds")
	}
	if arch != "arm64" && arch != "amd64" {
		return ReleaseAsset{}, errors.New("unsupported release architecture")
	}
	identity := m.Build
	identity.Arch = arch
	if err := identity.validate(channel, arch); err != nil {
		return ReleaseAsset{}, err
	}
	asset, ok := m.Assets[arch]
	expected := "dash-" + strings.ReplaceAll(m.Tag, "+", "-") + "-linux-" + arch + ".tar.gz"
	if !ok || asset.Name != expected || asset.Bytes <= 0 || asset.Bytes > maxReleaseArchive || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(asset.SHA256) {
		return ReleaseAsset{}, errors.New("release has no valid artifact for this architecture")
	}
	return asset, nil
}
func releaseClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedReleaseURL(request.URL) {
			return errors.New("untrusted release redirect")
		}
		return nil
	}}
}
func trustedReleaseURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func manifestURL(repository, channel, tag string) string {
	if tag == "" {
		tag = "channel-" + channel
	}
	return "https://github.com/" + repository + "/releases/download/" + url.PathEscape(tag) + "/manifest.json"
}
func readManifest(ctx context.Context, client *http.Client, repository, channel, tag string) (ReleaseManifest, error) {
	var manifest ReleaseManifest
	if !repositoryPattern.MatchString(repository) || !validChannel(channel) || (tag != "" && !releaseIDPattern.MatchString(tag)) {
		return manifest, errors.New("invalid release repository/channel/tag")
	}
	limited, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(limited, "GET", manifestURL(repository, channel, tag), nil)
	request.Header.Set("User-Agent", "dash-backend/"+buildVersion)
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request)
	if err != nil {
		return manifest, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return manifest, fmt.Errorf("release feed HTTP %d; this channel may not have been published yet", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return manifest, err
	}
	if len(data) > 64<<10 {
		return manifest, errors.New("release manifest exceeds 64 KiB")
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if tag != "" && manifest.Tag != tag {
		return manifest, errors.New("manifest tag mismatch")
	}
	return manifest, nil
}
func downloadRelease(ctx context.Context, client *http.Client, repository string, manifest ReleaseManifest, arch, archive string, progress func(int64, int64)) error {
	asset, err := manifest.asset(manifest.Channel, arch)
	if err != nil {
		return err
	}
	if !repositoryPattern.MatchString(repository) {
		return errors.New("invalid repository")
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://github.com/"+repository+"/releases/download/"+url.PathEscape(manifest.Tag)+"/"+asset.Name, nil)
	request.Header.Set("User-Agent", "dash-backend/"+buildVersion)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("release download HTTP %d", response.StatusCode)
	}
	if response.ContentLength > 0 && response.ContentLength != asset.Bytes {
		return errors.New("release length mismatch")
	}
	file, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	reader := &progressReader{ctx: ctx, reader: io.LimitReader(response.Body, asset.Bytes+1), total: asset.Bytes, progress: progress}
	size, err := io.CopyBuffer(io.MultiWriter(file, hash), reader, make([]byte, 256<<10))
	if err != nil {
		return err
	}
	if size != asset.Bytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("release checksum or length mismatch")
	}
	return file.Sync()
}

type progressReader struct {
	ctx         context.Context
	reader      io.Reader
	total, read int64
	progress    func(int64, int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if r.progress != nil {
		r.progress(r.read, r.total)
	}
	return n, err
}

// Only regular files from a fixed payload layout are accepted. No links,
// traversal, devices, external tar command or unbounded decompression.
func unpackRelease(ctx context.Context, archive, destination string, expected Build) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer zipped.Close()
	limited := &io.LimitedReader{R: checkedReader{ctx, zipped}, N: maxReleaseExpanded + 1}
	reader := tar.NewReader(limited)
	seen := map[string]bool{}
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, "./")
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." {
			if header.Typeflag == tar.TypeDir {
				continue
			}
		}
		if filepath.IsAbs(name) || strings.Contains(name, "\\") || filepath.ToSlash(filepath.Clean(name)) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] {
			return errors.New("unsafe or duplicate release path")
		}
		seen[name] = true
		count++
		if count > 2048 || header.Size < 0 || header.Size > maxReleaseExpanded || limited.N <= 0 {
			return errors.New("release exceeds extraction budget")
		}
		allowed := name == "dash-backend" || name == "release.json" || name == "dist" || strings.HasPrefix(name, "dist/") || name == "bin" || strings.HasPrefix(name, "bin/")
		if !allowed {
			return fmt.Errorf("unexpected release file %s", name)
		}
		target := filepath.Join(destination, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if name != "dist" && !strings.HasPrefix(name, "dist/") && name != "bin" && !strings.HasPrefix(name, "bin/") {
				return errors.New("unexpected release directory")
			}
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if name == "dist" || name == "bin" {
				return errors.New("dist must be a directory")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if name == "dash-backend" || name == "bin/ffmpeg" || name == "bin/ffprobe" {
				mode = 0700
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyBuffer(output, checkedReader{ctx, reader}, make([]byte, 128<<10))
			if copyErr == nil {
				copyErr = output.Sync()
			}
			output.Close()
			if copyErr != nil {
				return copyErr
			}
		default:
			return errors.New("release links and special files are forbidden")
		}
	}
	// Consume gzip's checksum/trailer; tar EOF alone does not validate it.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return err
	}
	if limited.N <= 0 {
		return errors.New("release exceeds 256 MiB expanded")
	}
	data, err := readSmallFile(filepath.Join(destination, "release.json"), 16<<10)
	if err != nil {
		return err
	}
	var actual Build
	if len(data) > 16<<10 || json.Unmarshal(data, &actual) != nil || actual != expected {
		return errors.New("archive identity does not match manifest")
	}
	if info, err := os.Stat(filepath.Join(destination, "dist", "index.html")); err != nil || !info.Mode().IsRegular() {
		return errors.New("PWA missing from release")
	}
	binary, err := elf.Open(filepath.Join(destination, "dash-backend"))
	if err != nil {
		return fmt.Errorf("invalid Linux binary: %w", err)
	}
	defer binary.Close()
	machine := elf.EM_AARCH64
	if expected.Arch == "amd64" {
		machine = elf.EM_X86_64
	}
	if binary.Class != elf.ELFCLASS64 || binary.Machine != machine {
		return errors.New("release binary architecture mismatch")
	}
	if _, err := os.Stat(filepath.Join(destination, "bin")); err == nil {
		return verifyMediaBundle(destination, expected.Arch)
	}
	return nil
}
func verifyMediaBundle(folder, arch string) error {
	data, err := readSmallFile(filepath.Join(folder, "bin/manifest.json"), 16<<10)
	if err != nil {
		return fmt.Errorf("media manifest: %w", err)
	}
	var manifest struct {
		Protocol int    `json:"protocol"`
		Arch     string `json:"arch"`
		Binaries map[string]struct {
			Bytes  int64  `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"binaries"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Protocol != 1 || manifest.Arch != arch {
		return errors.New("invalid media manifest")
	}
	machine := elf.EM_AARCH64
	if arch == "amd64" {
		machine = elf.EM_X86_64
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		path := filepath.Join(folder, "bin", name)
		binary, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("media %s: %w", name, err)
		}
		valid := binary.Class == elf.ELFCLASS64 && binary.Machine == machine
		for _, program := range binary.Progs {
			if program.Type == elf.PT_INTERP {
				valid = false
			}
		}
		needed, _ := binary.DynString(elf.DT_NEEDED)
		binary.Close()
		if !valid || len(needed) > 0 {
			return fmt.Errorf("media %s must be static Linux %s", name, arch)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, err := io.CopyBuffer(hash, input, make([]byte, 128<<10))
		input.Close()
		expected, exists := manifest.Binaries[name]
		if err != nil || !exists || size != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("media %s checksum mismatch", name)
		}
	}
	return nil
}
func atomicJSON(filename string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".dash-json-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(name, filename); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(filename))
}
func syncDirectory(folder string) error {
	file, err := os.Open(folder)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
func readBuild(folder string) (Build, error) {
	var build Build
	data, err := readSmallFile(filepath.Join(folder, "release.json"), 16<<10)
	if err != nil {
		return build, err
	}
	if len(data) > 16<<10 {
		return build, errors.New("release identity too large")
	}
	if err := json.Unmarshal(data, &build); err != nil {
		return build, err
	}
	if err := build.validate(build.Channel, runtime.GOARCH); err != nil {
		return build, err
	}
	if build.Tag != filepath.Base(folder) {
		return build, errors.New("release directory identity mismatch")
	}
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() {
		return build, errors.New("release directory must not be a symlink")
	}
	return build, nil
}

func readSmallFile(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("metadata file exceeds budget")
	}
	return data, nil
}
