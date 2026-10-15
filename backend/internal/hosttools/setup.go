package hosttools

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var sdkHosts = map[string]bool{"github.com": true, "release-assets.githubusercontent.com": true, "objects.githubusercontent.com": true}

func installNativeVP(archive, destination string) error {
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
	found := false
	temporary, err := os.CreateTemp(filepath.Dir(destination), "vp-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if entry.Name != "vp" {
			continue
		}
		if found || entry.Typeflag != tar.TypeReg || entry.Size <= 0 || entry.Size > 128<<20 {
			return fmt.Errorf("invalid native Vite+ archive")
		}
		found = true
		if _, err = io.Copy(temporary, reader); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("native Vite+ CLI is missing")
	}
	if err = temporary.Chmod(0755); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), destination)
}

// Setup provisions or checks Vite+ while leaving frontend runtime ownership to it.
func (t *Tools) Setup(check bool) error {
	var pkg struct {
		Dev     map[string]string `json:"devDependencies"`
		Manager string            `json:"packageManager"`
	}
	if err := readJSON(t.path("package.json"), &pkg, 1<<20); err != nil {
		return err
	}
	required := pkg.Dev["vite-plus"]
	if !versionPattern.MatchString(required) {
		return fmt.Errorf("pin an exact Vite+ version")
	}
	// Go is the host bootstrap runtime. Vite+ owns all JavaScript environment
	// provisioning; never inspect or execute a standalone node or pnpm command.
	data, err := t.command(t.Root, nil, "go", "version")
	if err != nil {
		return fmt.Errorf("install Go matching backend/go.mod: %w", err)
	}
	fmt.Println(strings.TrimSpace(string(data)))
	cli := "vp"
	home := t.path(".artifacts/toolchains/vite-plus")
	local := filepath.Join(home, "bin/vp")
	if _, err := os.Stat(local); err == nil {
		cli = local
	}
	installed, _ := t.command(t.Root, nil, cli, "--version")
	match := regexp.MustCompile(`(?m)^vp v` + regexp.QuoteMeta(required) + `$`)
	if !match.Match(installed) {
		if check {
			return fmt.Errorf("require Vite+ %s; run make setup-build-env", required)
		}
		if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || !validArch(runtime.GOARCH) {
			return fmt.Errorf("use a Linux/macOS ARM64 or AMD64 build host")
		}
		triple := "x86_64"
		if runtime.GOARCH == "arm64" {
			triple = "aarch64"
		}
		if runtime.GOOS == "darwin" {
			triple += "-apple-darwin"
		} else {
			triple += "-unknown-linux-gnu"
		}
		base := "https://github.com/voidzero-dev/vite-plus/releases/download/v" + required + "/"
		name := "vp-" + triple + ".tar.gz"
		stream, err := fetch(base+"vp-checksums.txt", sdkHosts)
		if err != nil {
			return err
		}
		checks, err := io.ReadAll(io.LimitReader(stream, 8<<20))
		stream.Close()
		if err != nil {
			return err
		}
		digest := ""
		for _, line := range strings.Split(string(checks), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
				digest = fields[0]
			}
		}
		if err = os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(home, "download-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		stream, err = fetch(base+name, sdkHosts)
		if err != nil {
			return err
		}
		archive := filepath.Join(tmp, "vp.tar.gz")
		err = checkedCopy(stream, archive, digest, -1, 256<<20)
		stream.Close()
		if err != nil {
			return err
		}
		if err = installNativeVP(archive, local); err != nil {
			return err
		}
		cli = local
	}
	env := []string{"VP_NO_UPDATE_CHECK=1"}
	if cli == local {
		env = append(env, "VP_HOME="+home, "VP_NODE_MANAGER=yes")
	}
	if !check {
		for _, args := range [][]string{{"env", "setup", "--refresh"}, {"env", "on"}, {"env", "install"}} {
			if err = t.run(env, cli, args...); err != nil {
				return err
			}
		}
	}
	data, err = t.command(t.Root, env, cli, "env", "current", "--json")
	if err != nil {
		return err
	}
	var state struct {
		Node struct {
			Version   string `json:"version"`
			Installed bool   `json:"installed"`
		} `json:"node"`
		Manager struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			Installed bool   `json:"installed"`
		} `json:"package_manager"`
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return err
	}
	pin, err := os.ReadFile(t.path(".node-version"))
	if err != nil {
		return err
	}
	if !state.Node.Installed || state.Node.Version != strings.TrimSpace(string(pin)) || !state.Manager.Installed || state.Manager.Name+"@"+state.Manager.Version != pkg.Manager {
		return fmt.Errorf("Vite+ managed environment is missing or out of date; run make setup-build-env")
	}
	if !check {
		directory := t.path(".artifacts/toolchains")
		if err = os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		contents := "# Generated by Dash; no shell profile changes.\nexport VP_NO_UPDATE_CHECK=1\n"
		if cli == local {
			quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
			contents += "export VP_HOME=" + quote(home) + "\nexport PATH=" + quote(filepath.Dir(local)) + ":\"$PATH\"\n"
		}
		if err = os.WriteFile(filepath.Join(directory, "env.sh"), []byte(contents), 0600); err != nil {
			return err
		}
	}
	fmt.Printf("Vite+ %s and its managed environment are ready. No standalone Node installation is needed.\n", required)
	return nil
}
