package hosttools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupUsesOnlyGoAndVitePlus(t *testing.T) {
	for _, installed := range []bool{true, false} {
		t.Run(fmt.Sprint(installed), func(t *testing.T) {
			root := t.TempDir()
			require(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"vite-plus":"1.2.0"},"packageManager":"pnpm@12.11.2"}`), 0600))
			require(t, os.WriteFile(filepath.Join(root, ".node-version"), []byte("24.19.0\n"), 0600))
			tools := New(root)
			tools.Execute = func(_ string, _ []string, name string, args ...string) ([]byte, error) {
				switch name {
				case "go":
					return []byte("go version go1.27.1 linux/arm64"), nil
				case "vp":
					if len(args) == 1 && args[0] == "--version" {
						return []byte("vp v1.2.0\n"), nil
					}
					if strings.Join(args, " ") != "env current --json" {
						t.Fatalf("unexpected Vite+ command: %v", args)
					}
					return []byte(fmt.Sprintf(`{"node":{"version":"24.19.0","installed":%t},"package_manager":{"name":"pnpm","version":"12.11.2","installed":true}}`, installed)), nil
				default:
					t.Fatalf("invoked a standalone runtime: %s", name)
					return nil, nil
				}
			}
			err := tools.Setup(true)
			assertThat(t, (err == nil) == installed, "incorrect managed-environment validation")
		})
	}
}
func TestInstallerOffroadExpressions(t *testing.T) {
	root, err := ProjectRoot()
	require(t, err)
	guards := []string{}
	for _, name := range []string{"scripts/install-backend.sh", "backend/install-service.sh", "backend/uninstall-service.sh"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		require(t, err)
		text := string(data)
		for {
			start := strings.Index(text, `[[ "$(cat /data/params/d/IsOffroad)`)
			if start < 0 {
				break
			}
			end := strings.Index(text[start:], " ]]") + start + 3
			assertThat(t, end > start, "unterminated guard")
			guards = append(guards, text[start:end])
			text = text[end:]
		}
	}
	assertThat(t, len(guards) == 5, "expected five installer driving gates")
	for _, scenario := range []struct {
		on, off                      string
		onExists, offExists, allowed bool
	}{{"", "1", false, true, true}, {"0", "1", true, true, true}, {"1", "1", true, true, false}, {"", "0", false, true, false}, {"0", "", true, false, false}, {"bad", "1", true, true, false}, {"", "bad", false, true, false}, {"0", "0", true, true, false}} {
		directory := t.TempDir()
		if scenario.onExists {
			require(t, os.WriteFile(filepath.Join(directory, "IsOnroad"), []byte(scenario.on), 0600))
		}
		if scenario.offExists {
			require(t, os.WriteFile(filepath.Join(directory, "IsOffroad"), []byte(scenario.off), 0600))
		}
		for _, guard := range guards {
			_, err := New(root).command(root, nil, "bash", "-c", strings.ReplaceAll(guard, "/data/params/d", directory))
			assertThat(t, (err == nil) == scenario.allowed, "incorrect offroad guard")
		}
	}
}

func TestCommandKeepsStructuredStdoutSeparate(t *testing.T) {
	binary, err := os.Executable()
	require(t, err)
	data, err := New(t.TempDir()).command(t.TempDir(), []string{"DASH_GO_TEST_HELPER=1", "GORACE=atexit_sleep_ms=0"}, binary, "emit")
	require(t, err)
	assertThat(t, strings.TrimSpace(string(data)) == `{"ok":true}`, "stderr polluted structured command output")
}
func TestMediaOfflineRequiresVerifiedCache(t *testing.T) {
	root := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(root, "tools/media"), 0755))
	for _, name := range []string{"build.sh", "Dockerfile"} {
		require(t, os.WriteFile(filepath.Join(root, "tools/media", name), []byte("fixture"), 0600))
	}
	require(t, os.WriteFile(filepath.Join(root, "tools/media/sources.json"), []byte(`{}`), 0600))
	t.Setenv("DASH_MEDIA_OFFLINE", "1")
	tools := New(root)
	tools.Execute = func(_ string, _ []string, name string, args ...string) ([]byte, error) {
		t.Fatalf("offline cache miss invoked %s", name)
		return nil, nil
	}
	_, err := tools.BuildMedia("arm64")
	assertThat(t, err != nil && strings.Contains(err.Error(), "verified media cache"), "offline mode accepted unverified media")
}
