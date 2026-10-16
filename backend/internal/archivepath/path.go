// Package archivepath validates paths relative to a chosen storage destination.
package archivepath

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Validate accepts an empty path for the destination root. It never permits
// escaping a USB volume, bucket or authorized cloud parent folder.
func Validate(value string) error {
	if value == "" {
		return nil
	}
	if !utf8.ValidString(value) || len(value) > 512 || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return errors.New("use a relative folder path inside the selected destination")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 180 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return errors.New("folder names cannot be empty, too long, or end with a dot or space")
		}
		for _, r := range part {
			if unicode.IsControl(r) || strings.ContainsRune(`\:*?"<>|`, r) {
				return errors.New("folder names contain unsupported characters")
			}
		}
		base := strings.ToLower(strings.Split(part, ".")[0])
		if base == "con" || base == "prn" || base == "aux" || base == "nul" || (len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9') {
			return errors.New("choose a folder name supported by Windows, exFAT and NTFS")
		}
	}
	return nil
}
