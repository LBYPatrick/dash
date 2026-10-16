package hosttools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseIdentity(t *testing.T) {
	revision := strings.Repeat("a", 40)
	for _, channel := range []string{"main", "nightly", "dev", "local"} {
		id, err := ReleaseIdentity(channel, "13", revision, "0.1.0", channel == "local")
		if err != nil {
			t.Fatal(err)
		}
		separator := "-"
		if channel == "main" {
			separator = "+"
		}
		expected := "v0.1.0" + separator + channel + ".13.aaaaaaaa"
		if channel == "local" {
			expected += ".dirty"
		}
		if id.Tag != expected {
			t.Fatalf("got %s", id.Tag)
		}
		next, err := ReleaseIdentity(channel, "14", revision, "0.1.0", false)
		if err != nil || id.Tag == next.Tag {
			t.Fatal("build identities must be unique")
		}
	}
	for _, channel := range []string{"feature/a", "stable", ""} {
		if _, err := ReleaseIdentity(channel, "1", revision, "0.1.0", false); err == nil {
			t.Fatal("accepted feature channel")
		}
	}
	for _, number := range []string{"0", "-1", "01", "1;echo no", "9007199254740992"} {
		if _, err := ReleaseIdentity("dev", number, revision, "0.1.0", false); err == nil {
			t.Fatal("accepted unsafe sequence", number)
		}
	}
	for _, bad := range []struct{ revision, version string }{{"unknown", "0.1.0"}, {revision, "0.1.0-dev"}} {
		if _, err := ReleaseIdentity("dev", "1", bad.revision, bad.version, false); err == nil {
			t.Fatal("accepted malformed identity")
		}
	}
}
func TestPublishRecovery(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		previous    int64
		mismatch    bool
		wantUploads int
		wantError   bool
		extra       bool
	}{{"retry feed only", 11, false, 1, false, false}, {"newer feed stays", 14, false, 0, false, false}, {"immutable mismatch", 11, true, 0, true, false}, {"metadata extension mismatch", 11, false, 0, true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, ".artifacts/release")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			id, _ := ReleaseIdentity("dev", "12", strings.Repeat("a", 40), "0.1.0", false)
			manifest := Manifest{Identity: id, Assets: map[string]Asset{"arm64": {Name: archiveName(id, "arm64"), Bytes: 1, SHA256: strings.Repeat("a", 64)}, "amd64": {Name: archiveName(id, "amd64"), Bytes: 1, SHA256: strings.Repeat("b", 64)}}}
			manifest.Installers = map[string]Asset{}
			for _, arch := range []string{"arm64", "amd64"} {
				manifest.Installers[arch] = Asset{Name: "dash-bootstrap-linux-" + arch, Bytes: 1, SHA256: strings.Repeat("a", 64)}
			}
			if err := writeJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GITHUB_REF_NAME", "dev")
			t.Setenv("GITHUB_SHA", id.Revision)
			uploads := 0
			tool := New(root)
			tool.Execute = func(_ string, _ []string, name string, args ...string) ([]byte, error) {
				if name != "gh" {
					t.Fatal("unexpected executable", name)
				}
				if len(args) < 3 {
					return nil, fmt.Errorf("unexpected arguments")
				}
				switch args[1] {
				case "view":
					names := []string{"manifest.json"}
					if !strings.HasPrefix(args[2], "channel-") {
						names = append(names, "SHA256SUMS", "bootstrap.sh", manifest.Assets["arm64"].Name, manifest.Assets["amd64"].Name, manifest.Installers["arm64"].Name, manifest.Installers["amd64"].Name)
					}
					assets := []map[string]string{}
					for _, name := range names {
						assets = append(assets, map[string]string{"name": name})
					}
					return json.Marshal(map[string]any{"isDraft": false, "assets": assets})
				case "download":
					data := manifest
					if strings.HasPrefix(args[2], "channel-") {
						data.Sequence = scenario.previous
					} else if scenario.mismatch {
						data.Revision = strings.Repeat("b", 40)
					}
					if scenario.extra && !strings.HasPrefix(args[2], "channel-") {
						raw, _ := json.Marshal(data)
						var document map[string]any
						json.Unmarshal(raw, &document)
						document["unexpected"] = true
						return json.Marshal(document)
					}
					return json.Marshal(data)
				case "upload":
					if args[2] != "channel-dev" {
						t.Fatal("rewrote immutable published bytes")
					}
					uploads++
					return nil, nil
				default:
					t.Fatal("unexpected release mutation", args)
					return nil, nil
				}
			}
			err := tool.Publish()
			if (err != nil) != scenario.wantError || uploads != scenario.wantUploads {
				t.Fatalf("error=%v uploads=%d", err, uploads)
			}
		})
	}
}
func TestReleaseRefusesLocalChannelWithoutSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"version":"0.1.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_REF_NAME", "local")
	t.Setenv("DASH_RELEASE_SEQUENCE", "1")
	t.Setenv("GITHUB_SHA", strings.Repeat("a", 40))
	if err := New(root).Release(false); err == nil {
		t.Fatal("accepted a source channel as a CI release")
	}
}
