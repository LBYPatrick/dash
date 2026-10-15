package hosttools

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var releaseHosts = map[string]bool{"github.com": true, "release-assets.githubusercontent.com": true, "objects.githubusercontent.com": true}
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// FetchOptions selects a release or an explicitly local source bundle.
type FetchOptions struct{ Channel, Release, Repository, Arch, Destination, LocalManifest string }

func validateIdentity(m Manifest, channel, arch, tag string) (Identity, error) {
	i := m.Identity
	suffix := "-" + channel + "."
	if channel == "main" {
		suffix = "+main."
	}
	if i.Protocol != 1 || i.Channel != channel || i.Sequence <= 0 || i.Sequence > maxSequence || !tagPattern.MatchString(i.Tag) || i.Tag != "v"+i.Version || !strings.Contains(i.Version, fmt.Sprintf("%s%d.", suffix, i.Sequence)) || !revisionPattern.MatchString(i.Revision) || (tag != "" && tag != i.Tag) || !validArch(arch) {
		return Identity{}, fmt.Errorf("invalid or incompatible release identity")
	}
	i.Arch = arch
	return i, nil
}

// FetchRelease verifies and atomically publishes a bounded, safe local payload.
func (t *Tools) FetchRelease(options FetchOptions) (Identity, error) {
	if !repositoryPattern.MatchString(options.Repository) || (options.Release != "" && !tagPattern.MatchString(options.Release)) {
		return Identity{}, fmt.Errorf("invalid repository or release tag")
	}
	if options.Channel != "main" && options.Channel != "nightly" && options.Channel != "dev" {
		return Identity{}, fmt.Errorf("invalid channel")
	}
	if _, err := os.Lstat(options.Destination); !os.IsNotExist(err) {
		return Identity{}, fmt.Errorf("destination already exists or cannot be inspected")
	}
	var manifest Manifest
	base := "https://github.com/" + options.Repository + "/releases/download/"
	channel := options.Channel
	if options.LocalManifest != "" {
		channel = "local"
		if err := readJSON(options.LocalManifest, &manifest, 65536); err != nil {
			return Identity{}, err
		}
	} else {
		locator := options.Release
		if locator == "" {
			locator = "channel-" + channel
		}
		stream, err := fetch(base+url.PathEscape(locator)+"/manifest.json", releaseHosts)
		if err != nil {
			return Identity{}, err
		}
		data, err := io.ReadAll(io.LimitReader(stream, 65537))
		stream.Close()
		if err != nil {
			return Identity{}, err
		}
		if len(data) > 65536 {
			return Identity{}, fmt.Errorf("manifest exceeds 64 KiB")
		}
		if err = json.Unmarshal(data, &manifest); err != nil {
			return Identity{}, err
		}
	}
	expected, err := validateIdentity(manifest, channel, options.Arch, options.Release)
	if err != nil {
		return Identity{}, err
	}
	asset, ok := manifest.Assets[options.Arch]
	if !ok || asset.Name != archiveName(expected, options.Arch) || asset.Bytes <= 0 || asset.Bytes > MaxArchive || !checksumPattern.MatchString(asset.SHA256) {
		return Identity{}, fmt.Errorf("invalid archive metadata")
	}
	parent := filepath.Dir(options.Destination)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return Identity{}, err
	}
	temporary, err := os.MkdirTemp(parent, "dash-release-*")
	if err != nil {
		return Identity{}, err
	}
	defer os.RemoveAll(temporary)
	var stream io.ReadCloser
	if options.LocalManifest != "" {
		stream, err = os.Open(filepath.Join(filepath.Dir(options.LocalManifest), asset.Name))
	} else {
		stream, err = fetch(base+url.PathEscape(expected.Tag)+"/"+asset.Name, releaseHosts)
	}
	if err != nil {
		return Identity{}, err
	}
	archive := filepath.Join(temporary, "download.tar.gz")
	err = checkedCopy(stream, archive, asset.SHA256, asset.Bytes, asset.Bytes)
	stream.Close()
	if err != nil {
		return Identity{}, err
	}
	payload := filepath.Join(temporary, "payload")
	if err = os.Mkdir(payload, 0700); err != nil {
		return Identity{}, err
	}
	if err = extractPayload(archive, payload, expected); err != nil {
		return Identity{}, err
	}
	if err = os.Rename(payload, options.Destination); err != nil {
		return Identity{}, err
	}
	return expected, nil
}
func extractPayload(archive, destination string, expected Identity) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	seen := map[string]bool{}
	var expanded int64
	count := 0
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 2048 {
			return fmt.Errorf("archive entry budget exceeded")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Name, "./"), "/")
		if (name == "" || name == ".") && entry.Typeflag == tar.TypeDir {
			continue
		}
		if !cleanPayloadPath(name) || path.Clean(name) != name || seen[name] || len(seen) >= 2048 {
			return fmt.Errorf("unsafe or duplicate archive path")
		}
		seen[name] = true
		allowed := name == "dash-backend" || name == "release.json" || name == "dist" || name == "bin" || strings.HasPrefix(name, "dist/") || strings.HasPrefix(name, "bin/")
		if !allowed {
			return fmt.Errorf("unexpected archive file")
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch entry.Typeflag {
		case tar.TypeDir:
			if name != "dist" && name != "bin" && !strings.HasPrefix(name, "dist/") && !strings.HasPrefix(name, "bin/") {
				return fmt.Errorf("unexpected archive directory")
			}
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if name == "dist" || name == "bin" || entry.Size < 0 || entry.Size > MaxExpanded-expanded {
				return fmt.Errorf("archive exceeds extraction budget")
			}
			expanded += entry.Size
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if name == "dash-backend" || name == "bin/ffmpeg" || name == "bin/ffprobe" {
				mode = 0700
			}
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, reader)
			closeErr := out.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive links and special files are forbidden")
		}
	}
	var actual Identity
	if err = readJSON(filepath.Join(destination, "release.json"), &actual, 16384); err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("archive identity mismatch")
	}
	if info, err := os.Stat(filepath.Join(destination, "dist/index.html")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("PWA missing from release")
	}
	if err = verifyELF(filepath.Join(destination, "dash-backend"), expected.Arch, false); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(destination, "bin")); err == nil {
		return verifyMedia(filepath.Join(destination, "bin"), expected.Arch)
	}
	return nil
}
