package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const testDrive = "00000001--abcdef0123"

func transferFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root, home, dest := t.TempDir(), t.TempDir(), t.TempDir()
	folder := filepath.Join(root, testDrive+"--0")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	return root, home, dest
}
func startManager(t *testing.T, root, home, dest string, parked *atomic.Bool, wrap func(Store) Store) *Manager {
	t.Helper()
	factory := func(_ context.Context, _ Config, source Source) (Store, error) {
		store, err := NewLocalStore(dest, source, "test-process")
		if err != nil {
			return nil, err
		}
		if wrap != nil {
			return wrap(store), nil
		}
		return store, nil
	}
	m, err := New(context.Background(), Options{Root: root, Home: home, State: func() string {
		if parked.Load() {
			return "parked"
		}
		return "onroad"
	}, Acquire: func(ctx context.Context) (func(), error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return func() {}, nil
	}, Factory: factory})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.configs.Save(Config{ID: "usb", Name: "Test USB", Kind: "usb", VolumeUUID: "DASH-TEST-0001"}); err != nil {
		m.Close()
		t.Fatal(err)
	}
	return m
}
func waitBatch(t *testing.T, m *Manager, id string, want string) Batch {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		b, err := m.batch(id)
		if err != nil {
			t.Fatal(err)
		}
		if b.Status == want {
			return b
		}
		if b.Status == "needs_attention" && want != "needs_attention" {
			t.Fatalf("transfer needs attention: %s", b.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := m.batch(id)
	t.Fatalf("timeout: want %s, got %+v", want, b)
	return b
}
func writeOriginal(t *testing.T, root, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, testDrive+"--0", name), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestBatchCopiesUnindexedOriginalsAndSkipsVerifiedCopies(t *testing.T) {
	root, home, dest := transferFixture(t)
	data := bytes.Repeat([]byte("camera with recorded audio"), 300)
	writeOriginal(t, root, "qcamera.ts", data)
	writeOriginal(t, root, "rlog.zst", []byte("closed but undecodable original bytes"))
	writeOriginal(t, root, "empty.hevc", nil)
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, testDrive+"--0", "link")); err != nil {
		t.Fatal(err)
	}
	var parked atomic.Bool
	parked.Store(true)
	m := startManager(t, root, home, dest, &parked, nil)
	defer m.Close()
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	complete := waitBatch(t, m, b.ID, "complete")
	if complete.CompletedFiles != 3 || complete.CompletedBytes != int64(len(data)+len("closed but undecodable original bytes")) {
		t.Fatal(complete)
	}
	file := filepath.Join(dest, "dash-archive/v1", m.sourceID, "sessions", testDrive, "originals", testDrive+"--0", "qcamera.ts")
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("original changed", err)
	}
	stat, _ := os.Stat(file)
	next, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	waitBatch(t, m, next.ID, "complete")
	after, _ := os.Stat(file)
	if !stat.ModTime().Equal(after.ModTime()) {
		t.Fatal("verified original was rewritten")
	}
	if _, err = os.Stat(filepath.Join(dest, "dash-archive/v1", m.sourceID, "snapshots", b.ID, "COMPLETE")); err != nil {
		t.Fatal(err)
	}
}
func TestBatchPreservesConflicts(t *testing.T) {
	root, home, dest := transferFixture(t)
	writeOriginal(t, root, "qcamera.ts", []byte("original"))
	var parked atomic.Bool
	parked.Store(false)
	m := startManager(t, root, home, dest, &parked, nil)
	defer m.Close()
	target := filepath.Join(dest, "dash-archive/v1", m.sourceID, "sessions", testDrive, "originals", testDrive+"--0", "qcamera.ts")
	os.MkdirAll(filepath.Dir(target), 0700)
	os.WriteFile(target, []byte("existing user data"), 0600)
	parked.Store(true)
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitBatch(t, m, b.ID, "complete_with_issues")
	if finished.Issues != 1 {
		t.Fatal(finished)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "existing user data" {
		t.Fatal("destination was overwritten")
	}
}

type blockingStore struct {
	Store
	started chan struct{}
}

func (s *blockingStore) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return a.Checkpoint, false, ctx.Err()
}
func TestIgnitionStopsActiveTransferAndManualPausePersists(t *testing.T) {
	root, home, dest := transferFixture(t)
	writeOriginal(t, root, "qcamera.ts", []byte("footage"))
	var parked atomic.Bool
	parked.Store(true)
	started := make(chan struct{}, 1)
	m := startManager(t, root, home, dest, &parked, func(s Store) Store { return &blockingStore{s, started} })
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("copy did not start")
	}
	parked.Store(false)
	waitBatch(t, m, b.ID, "waiting_parked")
	m.Close()
	m = startManager(t, root, home, dest, &parked, nil)
	defer m.Close()
	time.Sleep(300 * time.Millisecond)
	pending, _ := m.batch(b.ID)
	if pending.CompletedFiles != 0 {
		t.Fatal("transfer progressed while onroad")
	}
	if err = m.Action(b.ID, "pause"); err != nil {
		t.Fatal(err)
	}
	parked.Store(true)
	time.Sleep(350 * time.Millisecond)
	paused, _ := m.batch(b.ID)
	if paused.Status != "paused" {
		t.Fatal("manual pause was lost", paused)
	}
	if err = m.Action(b.ID, "resume"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, m, b.ID, "complete")
}
func TestLocalStepRecoversUncheckpointedBytes(t *testing.T) {
	root, _, dest := transferFixture(t)
	data := bytes.Repeat([]byte("abcdef"), int(ChunkBytes/6)+100)
	writeOriginal(t, root, "qcamera.ts", data)
	sourceRoot, _ := OpenDir(root)
	defer sourceRoot.Close()
	source := directorySource{sourceRoot}
	file, _ := source.Open(testDrive + "--0/qcamera.ts")
	sha, one, _ := Hash(context.Background(), file)
	modified := file.Modified.UnixNano()
	file.Close()
	a := Asset{Path: testDrive + "--0/qcamera.ts", Key: "dash-archive/v1/test/sessions/drive/originals/0/qcamera.ts", Size: int64(len(data)), Modified: modified, SHA256: sha, SHA1: one}
	store, _ := NewLocalStore(dest, source, "before-restart")
	_, done, err := store.Step(context.Background(), a)
	if err != nil || done {
		t.Fatal(err, done)
	}
	store.Close()
	store, _ = NewLocalStore(dest, source, "after-restart")
	defer store.Close()
	for i := 0; i < 5; i++ {
		cp, done, err := store.Step(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		a.Checkpoint = cp
		if done {
			return
		}
	}
	t.Fatal("interrupted copy did not resume")
}
func TestSafeDirectoryNeverFollowsSymlinksOrOverwrites(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	directory, _ := OpenDir(root)
	defer directory.Close()
	if file, err := directory.Open("escape/file", os.O_CREATE|os.O_WRONLY); err == nil {
		file.Close()
		t.Fatal("followed a symlink")
	}
	os.WriteFile(filepath.Join(root, "old"), []byte("old"), 0600)
	os.WriteFile(filepath.Join(root, "new"), []byte("new"), 0600)
	if err := directory.Publish("new", "old"); err == nil {
		t.Fatal("replaced existing data")
	}
	data, _ := os.ReadFile(filepath.Join(root, "old"))
	if string(data) != "old" {
		t.Fatal("existing file changed")
	}
}
func TestPublicDestinationsNeverContainCredentials(t *testing.T) {
	configs, _ := OpenConfigs(t.TempDir())
	config := Config{ID: "s3", Name: "Backup", Kind: "s3", Bucket: "bucket", Region: "us-east-1", Secret: Secret{AccessKey: "private-access-marker", SecretKey: "private-secret-marker", SessionToken: "private-session-marker"}}
	if err := configs.Save(config); err != nil {
		t.Fatal(err)
	}
	destinations, _ := configs.All()
	data, _ := json.Marshal(destinations)
	for _, secret := range []string{config.Secret.AccessKey, config.Secret.SecretKey, config.Secret.SessionToken} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("credential escaped")
		}
	}
	files, _ := os.ReadDir(configs.home)
	for _, file := range files {
		info, _ := file.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatal("private configuration is readable by others")
		}
	}
}
func TestChangedSourceIsNotMarkedComplete(t *testing.T) {
	root, home, dest := transferFixture(t)
	writeOriginal(t, root, "qcamera.ts", []byte("original"))
	var parked atomic.Bool
	parked.Store(true)
	var calls atomic.Int32
	m := startManager(t, root, home, dest, &parked, func(store Store) Store {
		return &changeSourceStore{Store: store, change: func() {
			if calls.Add(1) == 1 {
				writeOriginal(t, root, "qcamera.ts", []byte("changed during upload"))
			}
		}}
	})
	defer m.Close()
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	finished := waitBatch(t, m, b.ID, "complete_with_issues")
	if finished.Issues != 1 {
		t.Fatal(finished)
	}
}

type changeSourceStore struct {
	Store
	change func()
}

func (s *changeSourceStore) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	s.change()
	return Checkpoint{Offset: a.Size}, true, nil
}
func TestHashAndPortableNames(t *testing.T) {
	expected := sha256.Sum256([]byte("content"))
	hash, _, err := Hash(context.Background(), bytes.NewBufferString("content"))
	if err != nil || hash != hex.EncodeToString(expected[:]) {
		t.Fatal(hash, err)
	}
	if portable("CON") == "CON" || portable("log.") == "log." || portable("a:b") == "a:b" || portable("qcamera.ts") != "qcamera.ts" {
		t.Fatal("unsafe portable names")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = Hash(ctx, bytes.NewBufferString("data")); !errors.Is(err, context.Canceled) {
		t.Fatal("hash did not stop")
	}
}

type offlineCleanupStore struct {
	Store
	block *atomic.Bool
}

func (s *offlineCleanupStore) Step(ctx context.Context, a Asset) (Checkpoint, bool, error) {
	return a.Checkpoint, false, ErrDisconnected
}
func (s *offlineCleanupStore) Abort(ctx context.Context, a Asset) error {
	if s.block.Load() {
		return ErrDisconnected
	}
	return s.Store.Abort(ctx, a)
}
func TestCancelDisconnectedDestinationDoesNotBlockNewBatches(t *testing.T) {
	root, home, dest := transferFixture(t)
	writeOriginal(t, root, "qcamera.ts", []byte("original"))
	var parked, offline atomic.Bool
	parked.Store(true)
	offline.Store(true)
	m := startManager(t, root, home, dest, &parked, func(s Store) Store { return &offlineCleanupStore{s, &offline} })
	defer m.Close()
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	waitBatch(t, m, b.ID, "needs_attention")
	if err = m.Action(b.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	canceled := waitBatch(t, m, b.ID, "canceled")
	if !canceled.CleanupPending {
		t.Fatal("unreachable cleanup was forgotten")
	}
	next, err := m.Start("usb", false)
	if err != nil {
		t.Fatal("canceled unreachable destination blocked a new batch", err)
	}
	waitBatch(t, m, next.ID, "needs_attention")
	offline.Store(false)
	if err = m.Action(next.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	waitBatch(t, m, next.ID, "canceled")
}
