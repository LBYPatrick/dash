package hosttools

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

type mediaSource struct {
	File    string `json:"file"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}
type mediaManifest struct {
	Protocol int              `json:"protocol"`
	Arch     string           `json:"arch"`
	Inputs   string           `json:"inputs"`
	FFmpeg   string           `json:"ffmpeg"`
	X264     string           `json:"x264"`
	Binaries map[string]Asset `json:"binaries"`
}

func verifyELF(name, arch string, static bool) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 64)
	if _, err = io.ReadFull(f, header); err != nil {
		return err
	}
	machine := uint16(183)
	if arch == "amd64" {
		machine = 62
	}
	if !validArch(arch) || string(header[:6]) != "\x7fELF\x02\x01" || binary.LittleEndian.Uint16(header[18:20]) != machine {
		return fmt.Errorf("incorrect Linux executable architecture")
	}
	if static {
		offset := binary.LittleEndian.Uint64(header[32:40])
		size := binary.LittleEndian.Uint16(header[54:56])
		count := binary.LittleEndian.Uint16(header[56:58])
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if count > 128 || (count > 0 && size != 56) || offset > uint64(info.Size()) || uint64(count)*uint64(size) > uint64(info.Size())-offset {
			return fmt.Errorf("invalid executable program headers")
		}
		program := make([]byte, 56)
		for i := uint16(0); i < count; i++ {
			if _, err = f.ReadAt(program, int64(offset+uint64(i)*56)); err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(program) == 3 {
				return fmt.Errorf("media executable must be static")
			}
		}
	}
	return nil
}
func verifyMedia(directory, arch string) error {
	var manifest mediaManifest
	if err := readJSON(filepath.Join(directory, "manifest.json"), &manifest, 16384); err != nil {
		return err
	}
	if manifest.Protocol != 1 || manifest.Arch != arch {
		return fmt.Errorf("incorrect media manifest")
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		path := filepath.Join(directory, name)
		if err := verifyELF(path, arch, true); err != nil {
			return err
		}
		sum, size, err := digestFile(path)
		if err != nil {
			return err
		}
		expected := manifest.Binaries[name]
		if size != expected.Bytes || sum != expected.SHA256 {
			return fmt.Errorf("media checksum mismatch")
		}
	}
	return nil
}

// BuildMedia reuses a verified cache or builds pinned static media tools.
func (t *Tools) BuildMedia(arch string) (string, error) {
	if !validArch(arch) {
		return "", fmt.Errorf("unsupported media architecture")
	}
	var sources map[string]mediaSource
	if err := readJSON(t.path("tools/media/sources.json"), &sources, 65536); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, name := range []string{"sources.json", "build.sh", "Dockerfile"} {
		data, err := os.ReadFile(t.path("tools/media", name))
		if err != nil {
			return "", err
		}
		h.Write(data)
	}
	inputs := hex.EncodeToString(h.Sum(nil))
	output := t.path(".artifacts/media/linux-" + arch)
	var cached mediaManifest
	if readJSON(filepath.Join(output, "manifest.json"), &cached, 16384) == nil && cached.Inputs == inputs && verifyMedia(output, arch) == nil {
		return output, nil
	}
	if os.Getenv("DASH_MEDIA_OFFLINE") == "1" {
		return "", fmt.Errorf("verified media cache is unavailable; run make build-media with Docker first")
	}
	cache := t.path(".artifacts/media-source")
	if err := os.MkdirAll(cache, 0755); err != nil {
		return "", err
	}
	for _, source := range sources {
		if filepath.Base(source.File) != source.File || source.File == "." {
			return "", fmt.Errorf("invalid media source filename")
		}
		destination := filepath.Join(cache, source.File)
		if sum, _, err := digestFile(destination); err == nil && sum == source.SHA256 {
			continue
		}
		parsed, err := url.Parse(source.URL)
		if err != nil {
			return "", err
		}
		hosts := map[string]bool{parsed.Host: true}
		stream, err := fetch(source.URL, hosts)
		if err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(cache, "source-*")
		if err != nil {
			stream.Close()
			return "", err
		}
		temporary := tmp.Name()
		tmp.Close()
		os.Remove(temporary)
		err = checkedCopy(stream, temporary, source.SHA256, -1, 256<<20)
		stream.Close()
		if err != nil {
			os.Remove(temporary)
			return "", err
		}
		if err = os.Rename(temporary, destination); err != nil {
			return "", err
		}
	}
	if err := t.run(nil, "docker", "buildx", "build", "--platform", "linux/"+arch, "--build-context", "sources="+cache, "-f", "tools/media/Dockerfile", "--target", "bundle", "--output", "type=local,dest="+output, "."); err != nil {
		return "", err
	}
	for _, name := range []string{"Dockerfile", "sources.json"} {
		if err := copyFile(t.path("tools/media", name), filepath.Join(output, "sources", name)); err != nil {
			return "", err
		}
	}
	manifest := mediaManifest{Protocol: 1, Arch: arch, Inputs: inputs, FFmpeg: sources["ffmpeg"].Version, X264: sources["x264"].Version, Binaries: map[string]Asset{}}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err := verifyELF(filepath.Join(output, name), arch, true); err != nil {
			return "", err
		}
		sum, size, err := digestFile(filepath.Join(output, name))
		if err != nil {
			return "", err
		}
		manifest.Binaries[name] = Asset{Bytes: size, SHA256: sum}
	}
	if err := writeJSON(filepath.Join(output, "manifest.json"), manifest); err != nil {
		return "", err
	}
	return output, nil
}
