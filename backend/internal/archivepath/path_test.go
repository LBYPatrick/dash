package archivepath

import "testing"

func TestRelativePortableFolders(t *testing.T) {
	for _, path := range []string{"", "Drives/2026", "行车记录/十月", "family trips"} {
		if err := Validate(path); err != nil {
			t.Errorf("%q: %v", path, err)
		}
	}
	for _, path := range []string{"/tmp", "../outside", "a/../b", "a//b", "a/", "a\\b", "CON.txt", "a/NUL", "LPT1", "a.", "a ", "a\n", "a:b"} {
		if Validate(path) == nil {
			t.Errorf("accepted %q", path)
		}
	}
}
