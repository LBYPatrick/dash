// Package hosttools contains build-host workflows. It is not linked into the
// device backend; the device still receives only its binary and static assets.
package hosttools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Tools runs workflows relative to a checkout, with an injectable command runner.
type Tools struct {
	Root    string
	Execute func(string, []string, string, ...string) ([]byte, error)
}

// New creates build-host tooling for a checkout.
func New(root string) *Tools { return &Tools{Root: root} }

// ProjectRoot locates the checkout from the current working directory.
func ProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "backend/go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("run from the Dash checkout")
		}
		dir = parent
	}
}
func (t *Tools) path(parts ...string) string {
	return filepath.Join(append([]string{t.Root}, parts...)...)
}
func (t *Tools) command(dir string, env []string, name string, args ...string) ([]byte, error) {
	if t.Execute != nil {
		return t.Execute(dir, env, name, args...)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		data := append(stdout.Bytes(), stderr.Bytes()...)
		return data, fmt.Errorf("%s: %w\n%s", name, err, data)
	}
	// Structured stdout must remain parseable when gh, Go or Docker prints a
	// successful warning/progress message to stderr.
	if stderr.Len() > 0 {
		fmt.Fprint(os.Stderr, stderr.String())
	}
	return stdout.Bytes(), nil
}
func (t *Tools) run(env []string, name string, args ...string) error {
	data, err := t.command(t.Root, env, name, args...)
	fmt.Print(string(data))
	return err
}
func copyFile(source, dest string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", source)
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func copyTree(source, dest string) error { return os.CopyFS(dest, os.DirFS(source)) }
func readJSON(name string, target any, limit int64) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("JSON exceeds budget: %s", name)
	}
	return json.Unmarshal(data, target)
}
func writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(name, append(data, '\n'), 0600)
}
func digestFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

var checksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func requireURL(address string, hosts map[string]bool) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !hosts[u.Host] {
		return fmt.Errorf("untrusted HTTPS URL")
	}
	return nil
}
func fetch(address string, hosts map[string]bool) (io.ReadCloser, error) {
	if err := requireURL(address, hosts); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		return requireURL(req.URL.String(), hosts)
	}}
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dash-host/1")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, fmt.Errorf("download returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}
func checkedCopy(source io.Reader, dest string, digest string, expected, limit int64) error {
	if !checksumPattern.MatchString(digest) {
		return fmt.Errorf("invalid SHA-256")
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(source, limit+1))
	if err != nil {
		return err
	}
	if n > limit || (expected >= 0 && n != expected) || hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("download size or checksum mismatch")
	}
	return out.Sync()
}
func validArch(arch string) bool { return arch == "arm64" || arch == "amd64" }
func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func cleanPayloadPath(name string) bool {
	return name != "" && !strings.Contains(name, "\\") && !strings.HasPrefix(name, "/") && filepath.ToSlash(filepath.Clean(name)) == name && name != ".." && !strings.HasPrefix(name, "../")
}
func removeIfExists(name string) error {
	err := os.Remove(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
