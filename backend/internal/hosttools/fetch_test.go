package hosttools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testArchive(t *testing.T, root string, entries []*tar.Header, contents [][]byte) string {
	t.Helper()
	name := filepath.Join(root, "test.tar.gz")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	writer := tar.NewWriter(gz)
	for i, entry := range entries {
		if err = writer.WriteHeader(entry); err != nil {
			t.Fatal(err)
		}
		if len(contents) > i {
			if _, err = writer.Write(contents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func syntheticELF(arch string) []byte {
	data := make([]byte, 64)
	copy(data, []byte("\x7fELF\x02\x01"))
	machine := uint16(183)
	if arch == "amd64" {
		machine = 62
	}
	binary.LittleEndian.PutUint16(data[18:20], machine)
	return data
}
func TestTrustedDownloads(t *testing.T) {
	for _, hosts := range []map[string]bool{releaseHosts, sdkHosts} {
		for _, address := range []string{"http://github.com/a", "https://github.com.evil/a", "https://user@github.com/a", "https://github.com:123/a", "https://github.com:/a"} {
			if err := requireURL(address, hosts); err == nil {
				t.Fatal("accepted", address)
			}
		}
		if err := requireURL("https://release-assets.githubusercontent.com/a", hosts); err != nil {
			t.Fatal(err)
		}
	}
}
func TestVerifiedLocalBundleAndTampering(t *testing.T) {
	root := t.TempDir()
	id, _ := ReleaseIdentity("local", "123", strings.Repeat("a", 40), "0.1.0", true)
	id.Arch = "arm64"
	metadata, _ := json.Marshal(id)
	binary := syntheticELF("arm64")
	html := []byte("<html>local</html>")
	archive := testArchive(t, root, []*tar.Header{{Name: "dash-backend", Mode: 0700, Size: int64(len(binary))}, {Name: "dist/index.html", Size: int64(len(html))}, {Name: "release.json", Size: int64(len(metadata))}}, [][]byte{binary, html, metadata})
	name := archiveName(id, "arm64")
	if err := os.Rename(archive, filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	sum, size, err := digestFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Identity: id, Assets: map[string]Asset{"arm64": {Name: name, Bytes: size, SHA256: sum}}}
	m.Arch = ""
	manifest := filepath.Join(root, "manifest.json")
	if err = writeJSON(manifest, m); err != nil {
		t.Fatal(err)
	}
	options := FetchOptions{Channel: "main", Repository: "LBYPatrick/dash", Arch: "arm64", Destination: filepath.Join(root, "payload"), LocalManifest: manifest}
	actual, err := New(root).FetchRelease(options)
	if err != nil {
		t.Fatal(err)
	}
	if actual != id {
		t.Fatal("identity changed")
	}
	data, err := os.ReadFile(filepath.Join(options.Destination, "dist/index.html"))
	if err != nil || !bytes.Equal(data, html) {
		t.Fatal("payload changed")
	}
	file, err := os.OpenFile(filepath.Join(root, name), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("tampered")
	file.Close()
	options.Destination = filepath.Join(root, "rejected")
	if _, err = New(root).FetchRelease(options); err == nil {
		t.Fatal("accepted tampered archive")
	}
	if _, err = os.Stat(options.Destination); !os.IsNotExist(err) {
		t.Fatal("published rejected payload")
	}
	if _, err = validateIdentity(m, "main", "arm64", ""); err == nil {
		t.Fatal("crossed channel boundary")
	}
}
func TestUnsafeArchives(t *testing.T) {
	for _, entry := range []*tar.Header{{Name: "../outside"}, {Name: "dist/../../outside"}, {Name: "dist/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, {Name: "unexpected"}, {Name: "dist/link", Typeflag: tar.TypeLink, Linkname: "dash-backend"}, {Name: "dist/./file"}} {
		t.Run(entry.Name, func(t *testing.T) {
			root := t.TempDir()
			archive := testArchive(t, root, []*tar.Header{entry}, nil)
			dest := filepath.Join(root, "payload")
			os.Mkdir(dest, 0700)
			if err := extractPayload(archive, dest, Identity{}); err == nil {
				t.Fatal("accepted unsafe entry")
			}
			if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Fatal("wrote outside payload")
			}
		})
	}
}
func TestMediaVerification(t *testing.T) {
	directory := t.TempDir()
	data := syntheticELF("arm64")
	records := map[string]Asset{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		path := filepath.Join(directory, name)
		os.WriteFile(path, data, 0700)
		sum, size, err := digestFile(path)
		if err != nil {
			t.Fatal(err)
		}
		records[name] = Asset{Bytes: size, SHA256: sum}
	}
	if err := writeJSON(filepath.Join(directory, "manifest.json"), mediaManifest{Protocol: 1, Arch: "arm64", Binaries: records}); err != nil {
		t.Fatal(err)
	}
	if err := verifyMedia(directory, "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := verifyMedia(directory, "amd64"); err == nil {
		t.Fatal("accepted wrong architecture")
	}
	os.WriteFile(filepath.Join(directory, "ffmpeg"), append(data, 'x'), 0700)
	if err := verifyMedia(directory, "arm64"); err == nil {
		t.Fatal("accepted changed media")
	}
}
func TestNativeVPExtraction(t *testing.T) {
	for _, link := range []bool{false, true} {
		root := t.TempDir()
		cli := &tar.Header{Name: "vp", Size: 3, Mode: 0755}
		data := []byte("cli")
		if link {
			cli.Typeflag = tar.TypeSymlink
			cli.Linkname = "/etc/passwd"
			cli.Size = 0
			data = nil
		}
		archive := testArchive(t, root, []*tar.Header{cli, {Name: "../outside", Size: 1}}, [][]byte{data, []byte("x")})
		destination := filepath.Join(root, "vp")
		err := installNativeVP(archive, destination)
		if (err != nil) != link {
			t.Fatalf("link=%v error=%v", link, err)
		}
		if _, err = os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
			t.Fatal("extracted unrelated path")
		}
	}
}
