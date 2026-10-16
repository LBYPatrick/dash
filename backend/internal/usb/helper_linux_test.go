package usb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedDiskIncludesSiblingPartitions(t *testing.T) {
	root := t.TempDir()
	disk := filepath.Join(root, "sda")
	if err := os.MkdirAll(disk, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sda6", "sda12", "sda1"} {
		part := filepath.Join(disk, name)
		if err := os.Mkdir(part, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(part, "partition"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range []string{disk, filepath.Join(disk, "sda6"), filepath.Join(disk, "sda1"), filepath.Join(disk, "sda12")} {
		if wholeBlock(part) != disk {
			t.Fatal("system disk sibling escaped protection")
		}
	}
	external := filepath.Join(root, "sdb")
	if wholeBlock(external) == disk {
		t.Fatal("unrelated drive classified as internal")
	}
}
func TestUSBRejectsUntrustedDeviceNames(t *testing.T) {
	for _, name := range []string{"../sda", "sda/../../x", "sda;rm", "/dev/sda", "sda\n"} {
		if _, err := externalBlock(name); err == nil {
			t.Fatalf("accepted device name %q", name)
		}
	}
}
