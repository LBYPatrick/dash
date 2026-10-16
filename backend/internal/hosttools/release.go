package hosttools

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MaxArchive matches the device updater download budget.
const MaxArchive int64 = 128 << 20

// MaxExpanded bounds locally extracted device payloads.
const MaxExpanded int64 = 256 << 20
const maxSequence int64 = 9007199254740991

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Identity is the build metadata shared by bundles and the device supervisor.
type Identity struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Tag      string `json:"tag"`
	Channel  string `json:"channel"`
	Sequence int64  `json:"sequence"`
	Revision string `json:"revision"`
	Arch     string `json:"arch,omitempty"`
}

// Asset records the exact bytes and digest of a published file.
type Asset struct {
	Name   string `json:"name,omitempty"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Manifest identifies an immutable build and its architecture-specific assets.
type Manifest struct {
	Identity
	Assets     map[string]Asset `json:"assets"`
	Installers map[string]Asset `json:"installers,omitempty"`
}

// ReleaseIdentity validates channel inputs and derives a unique build identity.
func ReleaseIdentity(channel, sequence, revision, base string, dirty bool) (Identity, error) {
	number, err := strconv.ParseInt(sequence, 10, 64)
	if err != nil || number <= 0 || number > maxSequence || strconv.FormatInt(number, 10) != sequence || !revisionPattern.MatchString(revision) || !versionPattern.MatchString(base) {
		return Identity{}, fmt.Errorf("invalid build identity")
	}
	if channel != "main" && channel != "nightly" && channel != "dev" && channel != "local" {
		return Identity{}, fmt.Errorf("releases require main, nightly or dev")
	}
	separator := "-"
	if channel == "main" {
		separator = "+"
	}
	version := fmt.Sprintf("%s%s%s.%d.%s", base, separator, channel, number, revision[:8])
	if channel == "local" && dirty {
		version += ".dirty"
	}
	return Identity{Protocol: 1, Version: version, Tag: "v" + version, Channel: channel, Sequence: number, Revision: revision}, nil
}
func archiveName(identity Identity, arch string) string {
	return "dash-" + strings.ReplaceAll(identity.Tag, "+", "-") + "-linux-" + arch + ".tar.gz"
}

// Release packages native backends, the prebuilt PWA and verified media tools.
func (t *Tools) Release(source bool) error {
	var pkg struct {
		Version string `json:"version"`
	}
	if err := readJSON(t.path("package.json"), &pkg, 1<<20); err != nil {
		return err
	}
	channel := envDefault("GITHUB_REF_NAME", os.Getenv("DASH_RELEASE_CHANNEL"))
	sequence := envDefault("GITHUB_RUN_NUMBER", os.Getenv("DASH_RELEASE_SEQUENCE"))
	revision := os.Getenv("GITHUB_SHA")
	dirty := false
	if source {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			return fmt.Errorf("source builds are not channel CI releases")
		}
		channel = "local"
		sequence = envDefault("DASH_RELEASE_SEQUENCE", strconv.FormatInt(time.Now().UnixMilli(), 10))
		revision = ""
		data, err := t.command(t.Root, nil, "git", "status", "--porcelain")
		if err != nil {
			return err
		}
		dirty = len(strings.TrimSpace(string(data))) > 0
	}
	if revision == "" {
		data, err := t.command(t.Root, nil, "git", "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		revision = strings.TrimSpace(string(data))
	}
	if !source && channel == "local" {
		return fmt.Errorf("channel releases require main, nightly or dev")
	}
	identity, err := ReleaseIdentity(channel, sequence, revision, pkg.Version, dirty)
	if err != nil {
		return err
	}
	architectures := []string{"arm64", "amd64"}
	if source && os.Getenv("DASH_RELEASE_ARCH") != "" {
		architectures = []string{os.Getenv("DASH_RELEASE_ARCH")}
	}
	for _, arch := range architectures {
		if !validArch(arch) {
			return fmt.Errorf("unsupported build architecture")
		}
	}
	output := t.path(".artifacts/release")
	if err = os.RemoveAll(output); err != nil {
		return err
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		return err
	}
	manifest := Manifest{Identity: identity, Assets: map[string]Asset{}, Installers: map[string]Asset{}}
	var sums strings.Builder
	for _, arch := range architectures {
		stage, err := os.MkdirTemp(t.path(".artifacts"), "release-stage-"+arch+"-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(stage)
		flags := fmt.Sprintf("-s -w -X main.buildVersion=%s -X main.buildChannel=%s -X main.buildRevision=%s -X main.buildSequence=%s", identity.Version, channel, revision, sequence)
		registration, registrationErr := releaseRegistration()
		if registrationErr != nil {
			return registrationErr
		}
		if registration != "" {
			flags += " -X dash.local/backend/internal/archive.BundledRegistration=" + registration
		}
		data, err := t.command(t.path("backend"), []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch}, "go", "build", "-trimpath", "-ldflags", flags, "-o", filepath.Join(stage, "dash-backend"), ".")
		fmt.Print(string(data))
		if err != nil {
			return err
		}
		if err = copyTree(t.path("frontend/dist"), filepath.Join(stage, "dist")); err != nil {
			return err
		}
		media, err := t.BuildMedia(arch)
		if err != nil {
			return err
		}
		if err = copyTree(media, filepath.Join(stage, "bin")); err != nil {
			return err
		}
		filesystems, err := t.BuildFilesystems(arch)
		if err != nil {
			return err
		}
		if err = copyTree(filesystems, filepath.Join(stage, "bin/usb")); err != nil {
			return err
		}
		build := identity
		build.Arch = arch
		if err = writeJSON(filepath.Join(stage, "release.json"), build); err != nil {
			return err
		}
		name := archiveName(identity, arch)
		if err = writeArchive(stage, filepath.Join(output, name)); err != nil {
			return err
		}
		digest, size, err := digestFile(filepath.Join(output, name))
		if err != nil {
			return err
		}
		if size > MaxArchive {
			return fmt.Errorf("release archive exceeds updater limit")
		}
		manifest.Assets[arch] = Asset{Name: name, Bytes: size, SHA256: digest}
		fmt.Fprintf(&sums, "%s  %s\n", digest, name)
		if !source {
			name = "dash-bootstrap-linux-" + arch
			if err = copyFile(filepath.Join(stage, "dash-backend"), filepath.Join(output, name)); err != nil {
				return err
			}
			digest, size, err = digestFile(filepath.Join(output, name))
			if err != nil {
				return err
			}
			manifest.Installers[arch] = Asset{Name: name, Bytes: size, SHA256: digest}
			fmt.Fprintf(&sums, "%s  %s\n", digest, name)
		}
	}
	if !source {
		template, err := os.ReadFile(t.path("scripts/release-bootstrap.sh"))
		if err != nil {
			return err
		}
		script := strings.NewReplacer("__CHANNEL__", channel, "__TAG__", identity.Tag, "__ARM64_SHA__", manifest.Installers["arm64"].SHA256, "__AMD64_SHA__", manifest.Installers["amd64"].SHA256).Replace(string(template))
		if err = os.WriteFile(filepath.Join(output, "bootstrap.sh"), []byte(script), 0644); err != nil {
			return err
		}
		digest, _, err := digestFile(filepath.Join(output, "bootstrap.sh"))
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%s  bootstrap.sh\n", digest)
	}
	if err = writeJSON(filepath.Join(output, "manifest.json"), manifest); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(sums.String()), 0644); err != nil {
		return err
	}
	fmt.Printf("Packaged %s in .artifacts/release\n", identity.Tag)
	return nil
}

func releaseRegistration() (string, error) {
	googleID, googleSecret, microsoftID := os.Getenv("DASH_GOOGLE_CLIENT_ID"), os.Getenv("DASH_GOOGLE_CLIENT_SECRET"), os.Getenv("DASH_MICROSOFT_CLIENT_ID")
	if (googleID == "") != (googleSecret == "") {
		return "", fmt.Errorf("Google OAuth client id and client secret must be supplied together")
	}
	if googleID == "" && microsoftID == "" {
		return "", nil
	}
	data, err := json.Marshal(map[string]string{"googleClientId": googleID, "googleClientSecret": googleSecret, "microsoftClientId": microsoftID})
	if err != nil {
		return "", fmt.Errorf("OAuth application registration could not be encoded")
	}
	return base64.RawStdEncoding.EncodeToString(data), nil
}
func writeArchive(root, destination string) error {
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("release contains non-regular file")
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		if err = writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(writer, source)
		return err
	})
	if err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return gz.Close()
}

type releaseView struct {
	IsDraft bool `json:"isDraft"`
	Assets  []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

// Publish resumes immutable release publication before advancing a channel feed.
func (t *Tools) Publish() error {
	var manifest Manifest
	if err := readJSON(t.path(".artifacts/release/manifest.json"), &manifest, 65536); err != nil {
		return err
	}
	if (manifest.Channel != "main" && manifest.Channel != "nightly" && manifest.Channel != "dev") || os.Getenv("GITHUB_REF_NAME") != manifest.Channel || os.Getenv("GITHUB_SHA") != manifest.Revision {
		return fmt.Errorf("publishing requires a matching channel commit")
	}
	if _, err := validateIdentity(manifest, manifest.Channel, "arm64", manifest.Tag); err != nil {
		return err
	}
	if len(manifest.Assets) != 2 || len(manifest.Installers) != 2 {
		return fmt.Errorf("channel releases require both device architectures")
	}
	for arch, asset := range manifest.Assets {
		if !validArch(arch) || asset.Name != archiveName(manifest.Identity, arch) || asset.Bytes <= 0 || asset.Bytes > MaxArchive || !checksumPattern.MatchString(asset.SHA256) {
			return fmt.Errorf("invalid publication asset metadata")
		}
	}
	for arch, asset := range manifest.Installers {
		if !validArch(arch) || asset.Name != "dash-bootstrap-linux-"+arch || asset.Bytes <= 0 || asset.Bytes > 32<<20 || !checksumPattern.MatchString(asset.SHA256) {
			return fmt.Errorf("invalid bootstrap metadata")
		}
	}
	inspect := func(tag string) (*releaseView, error) {
		data, err := t.command(t.Root, nil, "gh", "release", "view", tag, "--json", "isDraft,assets")
		if err != nil {
			if regexp.MustCompile(`(?i)not found|HTTP 404`).MatchString(string(data)) {
				return nil, nil
			}
			return nil, err
		}
		var result releaseView
		err = json.Unmarshal(data, &result)
		return &result, err
	}
	documents := map[string]any{}
	var expectedDocument any
	if err := readJSON(t.path(".artifacts/release/manifest.json"), &expectedDocument, 65536); err != nil {
		return err
	}
	readManifest := func(tag string) (Manifest, error) {
		var result Manifest
		data, err := t.command(t.Root, nil, "gh", "release", "download", tag, "--pattern", "manifest.json", "--output", "-")
		if err == nil {
			if len(data) > 65536 {
				return result, fmt.Errorf("published manifest exceeds budget")
			}
			err = json.Unmarshal(data, &result)
			if err == nil {
				var document any
				err = json.Unmarshal(data, &document)
				documents[tag] = document
			}
		}
		return result, err
	}
	hasAsset := func(view *releaseView, name string) bool {
		for _, asset := range view.Assets {
			if asset.Name == name {
				return true
			}
		}
		return false
	}
	feed := "channel-" + manifest.Channel
	existingFeed, err := inspect(feed)
	if err != nil {
		return err
	}
	advance := true
	if existingFeed != nil && hasAsset(existingFeed, "manifest.json") {
		previous, err := readManifest(feed)
		if err != nil {
			return err
		}
		advance = previous.Sequence <= manifest.Sequence
	}
	names := []string{"manifest.json", "SHA256SUMS", "bootstrap.sh"}
	for _, arch := range []string{"arm64", "amd64"} {
		asset, ok := manifest.Assets[arch]
		if ok {
			names = append(names, asset.Name)
			names = append(names, manifest.Installers[arch].Name)
		}
	}
	existing, err := inspect(manifest.Tag)
	if err != nil {
		return err
	}
	if existing != nil && !existing.IsDraft {
		_, err := readManifest(manifest.Tag)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(documents[manifest.Tag], expectedDocument) {
			return fmt.Errorf("published release differs; use a new build number")
		}
		for _, name := range names {
			if !hasAsset(existing, name) {
				return fmt.Errorf("published release is missing assets")
			}
		}
	} else {
		if existing == nil {
			args := []string{"release", "create", manifest.Tag, "--target", manifest.Revision, "--draft", "--title", "Dash " + manifest.Version, "--notes", fmt.Sprintf("Channel: %s\nRevision: %s\nCI build: %d\n\nNative Go backend and prebuilt PWA. Install over SSH or use Settings → Updates.", manifest.Channel, manifest.Revision, manifest.Sequence)}
			if manifest.Channel != "main" {
				args = append(args, "--prerelease")
			}
			if err = t.run(nil, "gh", args...); err != nil {
				return err
			}
		}
		args := []string{"release", "upload", manifest.Tag}
		for _, name := range names {
			args = append(args, t.path(".artifacts/release", name))
		}
		args = append(args, "--clobber")
		if err = t.run(nil, "gh", args...); err != nil {
			return err
		}
		if err = t.run(nil, "gh", "release", "edit", manifest.Tag, "--draft=false", fmt.Sprintf("--latest=%t", advance && manifest.Channel == "main")); err != nil {
			return err
		}
	}
	if !advance {
		fmt.Println("A newer channel build is available; retaining its feed.")
		return nil
	}
	if existingFeed == nil {
		if err = t.run(nil, "gh", "release", "create", feed, "--target", manifest.Revision, "--prerelease", "--latest=false", "--title", "Dash "+manifest.Channel+" update feed", "--notes", "Rolling OTA manifest only. Use the immutable build tag in manifest.json for source and downloads."); err != nil {
			return err
		}
	}
	return t.run(nil, "gh", "release", "upload", feed, t.path(".artifacts/release/manifest.json"), t.path(".artifacts/release/bootstrap.sh"), "--clobber")
}
