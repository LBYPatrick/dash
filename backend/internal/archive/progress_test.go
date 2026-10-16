package archive

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransferRateExcludesReuseAndResetsWhenPaused(t *testing.T) {
	state := "parked"
	m := &Manager{options: Options{State: func() string { return state }}}
	now := time.Unix(100, 0)
	b := Batch{ID: "test", Status: "running", ProgressReady: true, TotalBytes: 1000}
	m.measure(&b, 0, 0, now)
	b.TransferredBytes = 400
	m.measure(&b, 200, 0, now.Add(2*time.Second))
	if b.BytesPerSecond != 100 || b.ETASeconds == nil || *b.ETASeconds != 6 {
		t.Fatal(b)
	}
	b.Status = "paused"
	b.BytesPerSecond, b.ETASeconds = 0, nil
	m.measure(&b, 200, 0, now.Add(3*time.Second))
	if len(m.meters) != 0 {
		t.Fatal("paused rate retained")
	}
	b.Status = "running"
	m.measure(&b, 200, 0, now.Add(100*time.Second))
	b.TransferredBytes = 800 // Reused bytes are verified, never counted as sent.
	m.measure(&b, 200, 0, now.Add(102*time.Second))
	if b.BytesPerSecond != 0 || b.ETASeconds != nil {
		t.Fatal(b)
	}
	state = "unknown"
	m.measure(&b, 200, 0, now.Add(103*time.Second))
	if len(m.meters) != 0 {
		t.Fatal("unknown driving state retained meter")
	}
}

func TestFolderCopyAndDurableTotals(t *testing.T) {
	root, home, dest := transferFixture(t)
	writeOriginal(t, root, "qcamera.ts", []byte("private original"))
	var parked atomic.Bool
	parked.Store(true)
	factory := func(_ context.Context, config Config, source Source) (Store, error) {
		return NewLocalStoreAt(dest, source, "process", config.Prefix)
	}
	options := Options{Root: root, Home: home, State: func() string {
		if parked.Load() {
			return "parked"
		}
		return "unknown"
	}, Acquire: func(ctx context.Context) (func(), error) { return func() {}, ctx.Err() }, Factory: factory}
	m, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.configs.Save(Config{ID: "usb", Name: "Archive", Kind: "usb", VolumeUUID: "DASH-TEST-0001", Prefix: "Drives/2026"}); err != nil {
		t.Fatal(err)
	}
	b, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	complete := waitBatch(t, m, b.ID, "complete")
	if complete.Directory != "Drives/2026" || !complete.ProgressReady || complete.TotalBytes != 16 || complete.TransferredBytes != 16 || complete.ReusedBytes != 0 {
		t.Fatal(complete)
	}
	var sent int64
	if err = m.db.QueryRow("SELECT sent_bytes FROM batch_totals WHERE batch=?", b.ID).Scan(&sent); err != nil || sent != 16 {
		t.Fatal(sent, err)
	}
	if _, err = os.Stat(filepath.Join(dest, "Drives/2026/dash-archive/v1", m.sourceID, "snapshots", b.ID, "COMPLETE")); err != nil {
		t.Fatal(err)
	}
	next, err := m.Start("usb", true)
	if err != nil {
		t.Fatal(err)
	}
	reused := waitBatch(t, m, next.ID, "complete")
	if reused.ReusedBytes != 16 {
		t.Fatal(reused)
	}
	if err = m.db.QueryRow("SELECT sent_bytes FROM batch_totals WHERE batch=?", next.ID).Scan(&sent); err != nil || sent != 0 {
		t.Fatal(sent, err)
	}
	// Simulate an older database without summaries. Opening while driving must
	// retain the inventory without aggregating it or starting any worker.
	_, err = m.db.Exec("DELETE FROM settings WHERE key='summary_version'")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	parked.Store(false)
	m, err = New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.totalsReady.Load() {
		t.Fatal("migration ran while driving state unknown")
	}
	if err = m.ensureTotals(context.Background()); err == nil {
		t.Fatal("unknown state allowed aggregation")
	}
	parked.Store(true)
	if err = m.ensureTotals(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := m.batch(next.ID)
	if err != nil || restored.ReusedBytes != 16 || restored.TransferredBytes != 16 || !restored.ProgressReady {
		t.Fatal(restored, err)
	}
}

func TestSaveFolderCannotEscapeThroughSymlink(t *testing.T) {
	root, _, dest := transferFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "Drives")); err != nil {
		t.Fatal(err)
	}
	source, err := OpenDir(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	store, err := NewLocalStoreAt(dest, directorySource{source}, "test", "Drives/2026")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Put(context.Background(), "dash-archive/v1/test/README.txt", []byte("test")); err == nil {
		t.Fatal("followed symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("modified external path", err)
	}
}
