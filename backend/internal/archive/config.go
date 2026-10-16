package archive

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Configs struct {
	home string
}

var configMu sync.Mutex

func (c *Configs) lock() (func(), error) {
	configMu.Lock()
	fd, err := unix.Open(filepath.Join(c.home, ".config.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		configMu.Unlock()
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX); err != nil {
		unix.Close(fd)
		configMu.Unlock()
		return nil, err
	}
	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd); configMu.Unlock() }, nil
}

func OpenConfigs(home string) (*Configs, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(home); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid private destination directory")
	}
	if err := os.Chmod(home, 0700); err != nil {
		return nil, err
	}
	return &Configs{home: home}, nil
}
func privateWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".private-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (c *Configs) Load(id string) (Config, error) {
	var result Config
	if !IDPattern.MatchString(id) {
		return result, errors.New("invalid destination id")
	}
	root, err := OpenDir(c.home)
	if err != nil {
		return result, err
	}
	defer root.Close()
	f, err := root.Open(id+".json", os.O_RDONLY)
	if err != nil {
		return result, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return Config{}, errors.New("invalid private destination configuration")
	}
	if result.ID != id {
		return Config{}, errors.New("destination id mismatch")
	}
	return result, result.Validate()
}
func (c *Configs) Save(config Config) error {
	config = withRegistration(config)
	if err := config.Validate(); err != nil {
		return err
	}
	unlock, err := c.lock()
	if err != nil {
		return err
	}
	defer unlock()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("destination configuration is too large")
	}
	return privateWrite(filepath.Join(c.home, config.ID+".json"), append(data, '\n'))
}
func (c *Configs) All() ([]Destination, error) {
	entries, err := os.ReadDir(c.home)
	if err != nil {
		return nil, err
	}
	result := []Destination{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		config, err := c.Load(entry.Name()[:len(entry.Name())-5])
		if err != nil {
			continue
		}
		result = append(result, config.Public())
	}
	return result, nil
}
func (c *Configs) UpdateSecret(expected Config, secret Secret) error {
	unlock, err := c.lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, err := c.Load(expected.ID)
	if err != nil {
		return err
	}
	if current.Kind != expected.Kind || current.ClientID != expected.ClientID || current.Tenant != expected.Tenant || current.Secret != expected.Secret {
		return errors.New("destination changed during authorization")
	}
	current.Secret = secret
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return privateWrite(filepath.Join(c.home, expected.ID+".json"), append(data, '\n'))
}

// UpdateDirectory preserves credentials refreshed concurrently by OAuth.
func (c *Configs) UpdateDirectory(id, directory string) (Destination, error) {
	unlock, err := c.lock()
	if err != nil {
		return Destination{}, err
	}
	defer unlock()
	config, err := c.Load(id)
	if err != nil {
		return Destination{}, err
	}
	config.Prefix = directory
	if err = config.Validate(); err != nil {
		return Destination{}, err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err == nil {
		err = privateWrite(filepath.Join(c.home, id+".json"), append(data, '\n'))
	}
	return config.Public(), err
}
func (c *Configs) Remove(id string) error {
	if !IDPattern.MatchString(id) {
		return errors.New("invalid destination id")
	}
	unlock, err := c.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return os.Remove(filepath.Join(c.home, id+".json"))
}
func fingerprint(config Config) string {
	config.Secret = Secret{}
	data, _ := json.Marshal(config)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func randomID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("system random source failed")
	}
	return hex.EncodeToString(data[:])
}
