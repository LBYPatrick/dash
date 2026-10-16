package hosttools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

type filesystemManifest struct {
	Protocol int              `json:"protocol"`
	Arch     string           `json:"arch"`
	Inputs   string           `json:"inputs"`
	Binaries map[string]Asset `json:"binaries"`
}

func verifyFilesystems(folder, arch string) error {
	var manifest filesystemManifest
	if err := readJSON(filepath.Join(folder, "manifest.json"), &manifest, 16384); err != nil {
		return err
	}
	if manifest.Protocol != 1 || manifest.Arch != arch {
		return fmt.Errorf("invalid USB filesystem manifest")
	}
	for _, name := range []string{"mount.exfat-fuse", "ntfs-3g"} {
		file := filepath.Join(folder, name)
		if err := verifyELF(file, arch, true); err != nil {
			return err
		}
		sum, size, err := digestFile(file)
		if err != nil {
			return err
		}
		expected := manifest.Binaries[name]
		if sum != expected.SHA256 || size != expected.Bytes {
			return fmt.Errorf("USB filesystem tool checksum mismatch")
		}
	}
	return nil
}
func (t *Tools) BuildFilesystems(arch string) (string, error) {
	if !validArch(arch) {
		return "", fmt.Errorf("unsupported USB filesystem architecture")
	}
	var sources map[string]mediaSource
	if err := readJSON(t.path("tools/filesystems/sources.json"), &sources, 65536); err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, name := range []string{"sources.json", "Dockerfile", "build.sh", "exfat-exclusive.patch"} {
		data, err := os.ReadFile(t.path("tools/filesystems", name))
		if err != nil {
			return "", err
		}
		hash.Write(data)
	}
	inputs := hex.EncodeToString(hash.Sum(nil))
	output := t.path(".artifacts/filesystems/linux-" + arch)
	var cached filesystemManifest
	if readJSON(filepath.Join(output, "manifest.json"), &cached, 16384) == nil && cached.Inputs == inputs && verifyFilesystems(output, arch) == nil {
		return output, nil
	}
	if os.Getenv("DASH_MEDIA_OFFLINE") == "1" {
		return "", fmt.Errorf("verified USB filesystem cache is unavailable; run make build-filesystems on a Linux/Docker build host")
	}
	cache := t.path(".artifacts/filesystem-source")
	if err := os.MkdirAll(cache, 0755); err != nil {
		return "", err
	}
	for _, source := range sources {
		if filepath.Base(source.File) != source.File {
			return "", fmt.Errorf("invalid filesystem source")
		}
		target := filepath.Join(cache, source.File)
		if sum, _, err := digestFile(target); err == nil && sum == source.SHA256 {
			continue
		}
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Scheme != "https" {
			return "", fmt.Errorf("invalid filesystem source URL")
		}
		hosts := map[string]bool{parsed.Host: true}
		if parsed.Host == "github.com" {
			for host := range releaseHosts {
				hosts[host] = true
			}
		}
		response, err := fetch(source.URL, hosts)
		if err != nil {
			return "", err
		}
		temporary, createErr := os.CreateTemp(cache, "source-*")
		if createErr != nil {
			response.Close()
			return "", createErr
		}
		temporaryPath := temporary.Name()
		temporary.Close()
		os.Remove(temporaryPath)
		err = checkedCopy(response, temporaryPath, source.SHA256, -1, 64<<20)
		response.Close()
		if err != nil {
			os.Remove(temporaryPath)
			return "", err
		}
		if err = os.Rename(temporaryPath, target); err != nil {
			os.Remove(temporaryPath)
			return "", err
		}
	}
	if err := t.run(nil, "docker", "buildx", "build", "--platform", "linux/"+arch, "--build-context", "sources="+cache, "-f", "tools/filesystems/Dockerfile", "--target", "bundle", "--output", "type=local,dest="+output, "."); err != nil {
		return "", err
	}
	for _, name := range []string{"Dockerfile", "sources.json"} {
		if err := copyFile(t.path("tools/filesystems", name), filepath.Join(output, "sources", name)); err != nil {
			return "", err
		}
	}
	manifest := filesystemManifest{Protocol: 1, Arch: arch, Inputs: inputs, Binaries: map[string]Asset{}}
	for _, name := range []string{"mount.exfat-fuse", "ntfs-3g"} {
		file := filepath.Join(output, name)
		if err := verifyELF(file, arch, true); err != nil {
			return "", err
		}
		sum, size, err := digestFile(file)
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
