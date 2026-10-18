package hosttools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOneLineInstaller(t *testing.T) {
	root, err := ProjectRoot()
	require(t, err)
	for _, name := range []string{"remote-install.sh", "release-bootstrap.sh"} {
		data, err := os.ReadFile(filepath.Join(root, "scripts", name))
		require(t, err)
		for _, scenario := range []struct {
			offroad, checksum string
			want              bool
		}{{"1", "good", true}, {"0", "good", false}, {"unknown", "good", false}, {"1", "bad", false}} {
			t.Run(name+"/"+scenario.offroad+"/"+scenario.checksum, func(t *testing.T) {
				directory := t.TempDir()
				require(t, os.MkdirAll(filepath.Join(directory, "recordings"), 0700))
				require(t, os.WriteFile(filepath.Join(directory, "IsOffroad"), []byte(scenario.offroad), 0600))
				binary := []byte("#!/bin/bash\nprintf '%s\\n' \"$@\" > \"$DASH_BOOTSTRAP_CALLS\"\n")
				require(t, os.WriteFile(filepath.Join(directory, "binary"), binary, 0700))
				digest := fmt.Sprintf("%x", sha256.Sum256(binary))
				if scenario.checksum == "bad" {
					digest = strings.Repeat("0", 64)
				}
				template, err := os.ReadFile(filepath.Join(root, "scripts/release-bootstrap.sh"))
				require(t, err)
				rewrite := func(script string) string {
					return strings.NewReplacer("/data/params/d", directory, "/data/media/0/realdata", filepath.Join(directory, "recordings"), "/data/dash", filepath.Join(directory, "dash"), "__CHANNEL__", "dev", "__TAG__", "v0.1.0-dev.2.aaaaaaaa", "__ARM64_SHA__", digest, "__AMD64_SHA__", digest).Replace(script)
				}
				require(t, os.WriteFile(filepath.Join(directory, "bootstrap.sh"), []byte(rewrite(string(template))), 0600))
				script := rewrite(string(data))
				// Only the shell network boundary is replaced. Real checksum verification runs.
				prefix := `uname() { if [[ "$1" == -s ]]; then echo Linux; else echo aarch64; fi; }
id() { echo comma; }
curl() {
  local url='' output=''
  while (( $# )); do case "$1" in https:*) url="$1"; shift ;; -o) output="$2"; shift 2 ;; *) shift ;; esac; done
  case "$url" in */bootstrap.sh) cp "$DASH_BOOTSTRAP_FIXTURE/bootstrap.sh" "$output" ;; */dash-bootstrap-linux-arm64) cp "$DASH_BOOTSTRAP_FIXTURE/binary" "$output" ;; *) return 99 ;; esac
}

export -f uname id curl
`
				entry := filepath.Join(directory, "entry.sh")
				require(t, os.WriteFile(entry, []byte(prefix+script), 0600))
				command := exec.Command("bash", entry, "--channel", "dev", "--release", "v0.1.0-dev.2.aaaaaaaa", "--runtime")
				calls := filepath.Join(directory, "calls")
				command.Env = append(os.Environ(), "DASH_BOOTSTRAP_FIXTURE="+directory, "DASH_BOOTSTRAP_CALLS="+calls)
				out, err := command.CombinedOutput()
				if (err == nil) != scenario.want {
					t.Fatalf("error=%v output=%s", err, out)
				}
				actual, readErr := os.ReadFile(calls)
				if scenario.want {
					require(t, readErr)
					if string(actual) != "--install-release\n--install-repo\nLBYPatrick/dash\n--install-channel\ndev\n--install-tag\nv0.1.0-dev.2.aaaaaaaa\n--install-runtime\n" {
						t.Fatalf("incorrect native arguments: %s", actual)
					}
				} else if readErr == nil {
					t.Fatal("executed installer after a rejected state or checksum")
				}
			})
		}
	}
}

func TestDeviceSSHCommandsDetectLostConnections(t *testing.T) {
	root, err := ProjectRoot()
	require(t, err)
	for _, scenario := range []struct {
		name string
		args []string
	}{
		{"install-backend.sh", []string{"--check", "comma@fixture"}},
		{"remote-install.sh", []string{"--target", "comma@fixture"}},
		{"uninstall-backend.sh", []string{"comma@fixture"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			capture := filepath.Join(directory, "arguments")
			require(t, os.WriteFile(filepath.Join(directory, "ssh"), []byte("#!/bin/bash\nprintf '%s\\n' \"$@\" > \"$DASH_SSH_CAPTURE\"\nexit 67\n"), 0700))
			command := exec.Command("bash", append([]string{filepath.Join(root, "scripts", scenario.name)}, scenario.args...)...)
			command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "DASH_SSH_CAPTURE="+capture)
			if err = command.Run(); err == nil {
				t.Fatal("installer ignored SSH failure")
			}
			data, err := os.ReadFile(capture)
			require(t, err)
			for _, option := range []string{"BatchMode=yes", "ConnectTimeout=10", "ServerAliveInterval=5", "ServerAliveCountMax=3"} {
				if !strings.Contains(string(data), "\n"+option+"\n") {
					t.Fatalf("missing connection bound %s", option)
				}
			}
		})
	}
}
