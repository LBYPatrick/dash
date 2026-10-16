package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestEmbeddedInstallerScripts(t *testing.T) {
	for _, name := range []string{"preflight-install.sh", "activate-install.sh", "install-service.sh", "systemd-registration.sh", "uninstall-service.sh", "boot-state.sh", "verify-service.sh"} {
		data, err := installFiles.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command("bash", "-n")
		command.Stdin = strings.NewReader(string(data))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
	}
}

func TestNativeInstallerRejectsInvalidInputsBeforePreflight(t *testing.T) {
	for _, args := range [][3]string{{"owner/repo;bad", "main", ""}, {"LBYPatrick/dash", "feature", ""}, {"LBYPatrick/dash", "main", "../../bad"}} {
		if err := installDeviceRelease(args[0], args[1], args[2], false); err == nil {
			t.Fatal("accepted invalid installer input")
		}
	}
}
