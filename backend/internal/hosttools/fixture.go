package hosttools

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func (t *Tools) Fixture() error {
	var manifest struct {
		Synthetic bool     `json:"synthetic"`
		Durations []int    `json:"segmentDurationsSeconds"`
		FPS       int      `json:"fps"`
		Drives    []string `json:"drives"`
		Files     map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := readJSON(t.path("scripts/fixtures/manifest.json"), &manifest, 1<<20); err != nil {
		return err
	}
	if !manifest.Synthetic || manifest.FPS <= 0 {
		return fmt.Errorf("expected a synthetic fixture manifest")
	}
	root := t.path(".artifacts/device")
	if err := os.MkdirAll(filepath.Join(root, "params"), 0755); err != nil {
		return err
	}
	for name, value := range map[string]string{"IsOnroad": "0", "IsOffroad": "1"} {
		if err := os.WriteFile(filepath.Join(root, "params", name), []byte(value), 0600); err != nil {
			return err
		}
	}
	for _, duration := range manifest.Durations {
		if duration <= 0 || duration > 120 {
			return fmt.Errorf("invalid synthetic duration")
		}
		base := filepath.Join(root, "fixture-media", strconv.Itoa(duration))
		if err := os.MkdirAll(base, 0755); err != nil {
			return err
		}
		input := []string{"-hide_banner", "-loglevel", "error", "-y", "-stream_loop", "-1", "-threads", "1", "-i", "frontend/public/sample-road.mp4", "-t", strconv.Itoa(duration), "-r", strconv.Itoa(manifest.FPS), "-vf", "scale=480:-2", "-filter_threads", "1", "-an"}
		for _, camera := range []struct {
			name, codec, format string
			extra               []string
		}{{"qcamera.ts", "libx264", "mpegts", []string{"-bf", "0", "-g", "20"}}, {"fcamera.hevc", "libx265", "hevc", []string{"-x265-params", "pools=none:frame-threads=1:log-level=error:bframes=0:keyint=20"}}} {
			args := append(append([]string{}, input...), "-c:v", camera.codec, "-threads", "1", "-preset", "ultrafast")
			args = append(args, camera.extra...)
			args = append(args, "-pix_fmt", "yuv420p", "-f", camera.format, filepath.Join(base, camera.name))
			if err := t.run(nil, "ffmpeg", args...); err != nil {
				return err
			}
		}
	}
	for _, id := range []string{"2026-10-12--17-42-08", "2026-10-12--08-16-32", "2026-10-11--16-05-11"} {
		for _, segment := range []string{"0", "1"} {
			if err := os.RemoveAll(filepath.Join(root, "recordings", id+"--"+segment)); err != nil {
				return err
			}
		}
	}
	for _, id := range manifest.Drives {
		if filepath.Base(id) != id || id == "." || id == ".." {
			return fmt.Errorf("invalid fixture drive")
		}
		for segment, duration := range manifest.Durations {
			directory := fmt.Sprintf("%s--%d", id, segment)
			destination := filepath.Join(root, "recordings", directory)
			if err := os.MkdirAll(destination, 0755); err != nil {
				return err
			}
			for _, name := range []string{"qlog.bz2", "qlog", "rlog.bz2", "rlog"} {
				if err := removeIfExists(filepath.Join(destination, name)); err != nil {
					return err
				}
			}
			for _, kind := range []string{"qlog", "rlog"} {
				name := "device/" + directory + "/" + kind + ".zst"
				source := t.path("scripts/fixtures", name)
				digest, _, err := digestFile(source)
				if err != nil {
					return err
				}
				if digest != manifest.Files[name].SHA256 {
					return fmt.Errorf("fixture checksum mismatch: %s", name)
				}
				if err = copyFile(source, filepath.Join(destination, kind+".zst")); err != nil {
					return err
				}
			}
			base := filepath.Join(root, "fixture-media", strconv.Itoa(duration))
			if err := copyFile(filepath.Join(base, "qcamera.ts"), filepath.Join(destination, "qcamera.ts")); err != nil {
				return err
			}
			for _, camera := range []string{"fcamera.hevc", "ecamera.hevc", "dcamera.hevc"} {
				if err := copyFile(filepath.Join(base, "fcamera.hevc"), filepath.Join(destination, camera)); err != nil {
					return err
				}
			}
		}
	}
	fmt.Println("Synthetic device ready: native zstd cereal logs, 60 + 8 seconds, 20 fps.")
	return nil
}
