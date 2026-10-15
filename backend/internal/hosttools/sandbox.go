package hosttools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SandboxUp seeds only project module archives, then starts the Docker sandbox.
func (t *Tools) SandboxUp() error {
	cache := t.path(".artifacts/docker-go-cache")
	env := []string{}
	err := func() error {
		data, err := t.command(t.Root, nil, "go", "env", "GOMODCACHE")
		if err != nil {
			return err
		}
		download := filepath.Join(strings.TrimSpace(string(data)), "cache/download")
		data, err = t.command(t.path("backend"), nil, "go", "mod", "download", "-json", "all")
		if err != nil {
			return err
		}
		if err = os.RemoveAll(cache); err != nil {
			return err
		}
		if err = os.MkdirAll(cache, 0755); err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		count := 0
		for {
			var module struct{ Info, GoMod, Zip, Error string }
			if err = decoder.Decode(&module); err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if module.Error != "" {
				return fmt.Errorf("%s", module.Error)
			}
			files := []string{module.Info, module.GoMod}
			if module.Zip != "" {
				files = append(files, module.Zip, module.Zip+"hash")
			}
			for _, source := range files {
				if source == "" {
					continue
				}
				relative, err := filepath.Rel(download, source)
				if err != nil || !cleanPayloadPath(filepath.ToSlash(relative)) {
					return fmt.Errorf("module outside download cache")
				}
				if err = copyFile(source, filepath.Join(cache, relative)); err != nil {
					return err
				}
			}
			count++
		}
		fmt.Printf("Seeded %d project module archives for the Linux build.\n", count)
		return nil
	}()
	if err != nil {
		fmt.Printf("Local Go cache unavailable; Docker will download dependencies normally (%v).\n", err)
	} else {
		env = append(env, "DASH_GO_CACHE_CONTEXT="+cache)
	}
	return t.run(env, "docker", "compose", "up", "--build", "-d", "--wait")
}
