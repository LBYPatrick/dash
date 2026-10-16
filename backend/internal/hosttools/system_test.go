package hosttools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The same compiled Go test binary doubles as fake system tools in isolated
// shell fixtures. No interpreter, sudo, firmware or host systemd is involved.
func TestMain(m *testing.M) {
	if os.Getenv("DASH_GO_TEST_HELPER") == "1" || (os.Getenv("DASH_INSTALL_TEST_ROOT") != "" && (filepath.Base(os.Args[0]) == "ssh" || filepath.Base(os.Args[0]) == "scp")) {
		if err := testHelper(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func testHelper(args []string) error {
	name := filepath.Base(os.Args[0])
	if len(args) > 0 && (args[0] == "rewrite" || args[0] == "assert-before" || args[0] == "atomic" || args[0] == "emit") {
		name = args[0]
		args = args[1:]
	}
	switch name {
	case "emit":
		fmt.Fprintln(os.Stdout, `{"ok":true}`)
		fmt.Fprintln(os.Stderr, "fixture warning")
		return nil
	case "ssh":
		root := os.Getenv("DASH_INSTALL_TEST_ROOT")
		if !strings.Contains(strings.Join(args, " "), "comma@fixture") {
			return fmt.Errorf("refusing non-fixture SSH target")
		}
		data, _ := json.Marshal(args)
		if err := appendTestLog(filepath.Join(root, "ssh.jsonl"), string(data)); err != nil {
			return err
		}
		if len(args) >= 2 && strings.Join(args[len(args)-2:], " ") == "uname -m" {
			fmt.Println("aarch64")
			return nil
		}
		script, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		if len(script) > 0 {
			name := "preflight.sh"
			if bytesContain(script, "DASH_PREVIOUS=") {
				name = "activate.sh"
			}
			return os.WriteFile(filepath.Join(root, name), script, 0600)
		}
		return nil
	case "scp":
		if len(args) < 2 {
			return fmt.Errorf("missing scp arguments")
		}
		source, target := args[len(args)-2], args[len(args)-1]
		if !strings.HasPrefix(target, "comma@fixture:/data/dash/") {
			return fmt.Errorf("refusing non-fixture SCP target")
		}
		directory := filepath.Join(os.Getenv("DASH_INSTALL_TEST_ROOT"), "transfer")
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		if strings.HasSuffix(source, "/.") {
			return copyTree(source, filepath.Join(directory, "payload"))
		}
		return copyFile(source, filepath.Join(directory, filepath.Base(target)))
	case "rewrite":
		if len(args) < 3 {
			return fmt.Errorf("rewrite requires source, target, root")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		text := string(data)
		for _, prefix := range []string{"/etc/systemd/system", "/run/systemd/system", "/data/dash", "/run/dash"} {
			text = strings.ReplaceAll(text, prefix, args[2]+prefix)
		}
		return os.WriteFile(args[1], []byte(text), 0600)
	case "assert-before":
		if len(args) != 3 {
			return fmt.Errorf("assert-before requires log and two lines")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		first, second := -1, -1
		for i, line := range lines {
			if line == args[1] && first < 0 {
				first = i
			}
			if line == args[2] && second < 0 {
				second = i
			}
		}
		if first < 0 || second <= first {
			return fmt.Errorf("publication order is incorrect")
		}
		return nil
	case "stat":
		info, err := os.Lstat(args[len(args)-1])
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		fmt.Printf("%d:%d:%d:%d:%d\n", stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano(), info.ModTime().UnixNano())
		return nil
	case "atomic":
		if len(args) == 0 {
			return fmt.Errorf("missing atomic operation")
		}
		operation := args[0]
		args = args[1:]
		phase := operation
		if operation == "sync" {
			info, err := os.Stat(args[len(args)-1])
			if err != nil {
				return err
			}
			phase = "sync-file"
			if info.IsDir() {
				phase = "sync-dir"
			}
		}
		if err := appendTestLog(os.Getenv("DASH_ATOMIC_LOG"), phase); err != nil {
			return err
		}
		if os.Getenv("DASH_ATOMIC_FAIL") == phase {
			if operation == "install" {
				os.WriteFile(args[len(args)-1], []byte("incomplete"), 0600)
			}
			return fmt.Errorf("injected atomic failure")
		}
		if operation == "sync" {
			info, _ := os.Stat(args[len(args)-1])
			if info.IsDir() {
				return nil
			}
		}
		name = operation
	}
	if log := os.Getenv("DASH_REGISTRATION_LOG"); log != "" {
		if err := appendTestLog(log, name+" "+strings.Join(args, " ")); err != nil {
			return err
		}
	}
	switch name {
	case "findmnt":
		for i, arg := range args {
			if arg == "-o" && i+1 < len(args) {
				fmt.Println(map[string]string{"TARGET": "/", "FSTYPE": "ext4", "OPTIONS": "ro,relatime"}[args[i+1]])
				return nil
			}
		}
		return fmt.Errorf("unexpected findmnt")
	case "mount":
		if strings.Join(args, " ") != "-o remount,rw /" && strings.Join(args, " ") != "-o remount,ro /" {
			return fmt.Errorf("unexpected mount")
		}
		return nil
	case "install":
		mode := os.FileMode(0755)
		directory := false
		paths := []string{}
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-o", "-g":
				i++
			case "-m":
				i++
				value, err := strconv.ParseUint(args[i], 8, 32)
				if err != nil {
					return err
				}
				mode = os.FileMode(value)
			case "-d":
				directory = true
			default:
				paths = append(paths, args[i])
			}
		}
		if directory {
			for _, path := range paths {
				if err := os.MkdirAll(path, mode); err != nil {
					return err
				}
			}
			return nil
		}
		if len(paths) != 2 {
			return fmt.Errorf("unexpected install paths")
		}
		if err := copyFile(paths[0], paths[1]); err != nil {
			return err
		}
		return os.Chmod(paths[1], mode)
	case "sync":
		if os.Getenv("DASH_TEST_FAIL_SYNC") == "1" && strings.HasSuffix(args[len(args)-1], ".next") {
			return fmt.Errorf("injected sync failure")
		}
		for _, path := range args {
			if path == "--" {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			err = f.Sync()
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	case "mv":
		return os.Rename(args[len(args)-2], args[len(args)-1])
	case "readlink":
		if len(args) > 1 && args[0] == "-f" {
			path := args[len(args)-1]
			for i := 0; i < 20; i++ {
				target, err := os.Readlink(path)
				if err != nil {
					break
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(path), target)
				}
				path = filepath.Clean(target)
			}
			result, err := filepath.EvalSymlinks(path)
			if err != nil {
				result, err = filepath.Abs(path)
			}
			if err != nil {
				return err
			}
			fmt.Println(result)
			return nil
		}
		target, err := os.Readlink(args[len(args)-1])
		if err == nil {
			fmt.Println(target)
		}
		return err
	case "systemctl":
		runtime := false
		action := ""
		for _, arg := range args {
			if arg == "--runtime" {
				runtime = true
			}
			if !strings.HasPrefix(arg, "-") && action == "" {
				action = arg
			}
		}
		if action == "enable" && os.Getenv("DASH_TEST_FAIL_ENABLE") == "1" {
			return fmt.Errorf("injected enable failure")
		}
		if action == "disable" {
			layer := "etc"
			if runtime {
				layer = "run"
			}
			directory := filepath.Join(os.Getenv("DASH_TEST_ROOT"), layer, "systemd/system")
			if _, err := os.Stat(filepath.Join(directory, "dash.service")); err != nil {
				return fmt.Errorf("unit missing before disable")
			}
			return removeIfExists(filepath.Join(directory, "multi-user.target.wants/dash.service"))
		}
		return nil
	default:
		return fmt.Errorf("unexpected fixture tool %s", name)
	}
}
func appendTestLog(name, line string) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}
func shellFixture(t *testing.T, name string) {
	t.Helper()
	root, err := ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(root, "scripts", name))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DASH_GO_TEST_HELPER=1", "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0", "DASH_TEST_HELPER="+binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}
func TestSystemdRegistration(t *testing.T) { shellFixture(t, "systemd-registration.test.sh") }
func TestBootState(t *testing.T)           { shellFixture(t, "boot-state.test.sh") }
func TestServiceVerification(t *testing.T) { shellFixture(t, "verify-service.test.sh") }
func TestInstallerPreflight(t *testing.T)  { shellFixture(t, "install-backend.test.sh") }
func TestAtomicInstall(t *testing.T) {
	root, err := ProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(root, "backend/activate-install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(script), "dash_install_atomic() {")
	end := strings.Index(string(script)[start:], "\n}") + start + 2
	function := string(script)[start:end]
	fixture := t.TempDir()
	binary, _ := os.Executable()
	source := filepath.Join(fixture, "payload")
	destination := filepath.Join(fixture, "bootstrap")
	os.WriteFile(source, []byte("complete new executable"), 0700)
	for _, failure := range []string{"install", "sync-file", "mv", "sync-dir", ""} {
		t.Run("phase-"+failure, func(t *testing.T) {
			os.WriteFile(destination, []byte("complete old executable"), 0700)
			log := filepath.Join(fixture, "atomic.log")
			os.WriteFile(log, nil, 0600)
			cmd := exec.Command("bash", "-c", "set -euo pipefail\nsudo() { [[ \"$1\" == -n ]]; shift; \"$DASH_TEST_HELPER\" atomic \"$@\"; }\n"+function+"\ndash_install_atomic \"$1\" \"$2\" 755", "bash", source, destination)
			cmd.Env = append(os.Environ(), "DASH_GO_TEST_HELPER=1", "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0", "DASH_TEST_HELPER="+binary, "DASH_ATOMIC_LOG="+log, "DASH_ATOMIC_FAIL="+failure)
			out, err := cmd.CombinedOutput()
			if (err == nil) != (failure == "") {
				t.Fatalf("failure=%s err=%v %s", failure, err, out)
			}
			actual, _ := os.ReadFile(destination)
			expected := "complete old executable"
			if failure == "sync-dir" || failure == "" {
				expected = "complete new executable"
			}
			if string(actual) != expected {
				t.Fatal("partial executable replaced durable bootstrap")
			}
			if failure == "" {
				data, _ := os.ReadFile(log)
				if string(data) != "install\nsync-file\nmv\nsync-dir\n" {
					t.Fatal("incorrect flush order")
				}
			}
		})
	}
}

func bytesContain(data []byte, text string) bool { return strings.Contains(string(data), text) }
