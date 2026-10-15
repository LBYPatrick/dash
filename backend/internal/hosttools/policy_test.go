package hosttools

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Keep the developer-facing runtime contract in CI as new workflows are added.
func TestHostRuntimePolicy(t *testing.T) {
	root, err := ProjectRoot()
	require(t, err)
	require(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".artifacts", "node_modules", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".mjs") {
			t.Errorf("standalone module script is forbidden: %s", path)
		}
		return nil
	}))
	directNode := regexp.MustCompile(`(^|[\s;&|])node[\s]`)
	for _, name := range []string{"Makefile", "Dockerfile", ".github/workflows/channels.yml", "scripts/setup-build-env.sh", "scripts/install-backend.sh"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		require(t, err)
		if strings.Contains(string(data), ".mjs") || directNode.Match(data) {
			t.Errorf("%s invokes a standalone Node script", name)
		}
	}
}
