package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"dash.local/backend/internal/archive/archivepb"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	_ "modernc.org/sqlite"
)

type Manager struct {
	options      Options
	db           *sql.DB
	configs      *Configs
	source       directorySource
	sourceID     string
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	mu           sync.Mutex
	activeCancel context.CancelFunc
	activeID     string
	cleanupTried map[string]time.Time
}

func New(ctx context.Context, options Options) (*Manager, error) {
	if options.State == nil || options.Acquire == nil || options.Factory == nil {
		return nil, errors.New("transfer dependencies are missing")
	}
	if err := os.MkdirAll(options.Home, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(options.Home); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("unsafe transfer state directory")
	}
	if err := os.Chmod(options.Home, 0700); err != nil {
		return nil, err
	}
	configs, err := OpenConfigs(filepath.Join(options.Home, "destinations"))
	if err != nil {
		return nil, err
	}
	root, err := OpenDir(options.Root)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(options.Home, "transfers.sqlite"))
	if err != nil {
		root.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout=3000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL",
		`CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS batches(id TEXT PRIMARY KEY,destination TEXT NOT NULL,fingerprint TEXT NOT NULL,status TEXT NOT NULL,message TEXT NOT NULL,created INTEGER NOT NULL,auto INTEGER NOT NULL,scanned INTEGER NOT NULL DEFAULT 0,cleanup INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS files(batch TEXT NOT NULL,path TEXT NOT NULL,key TEXT NOT NULL,session TEXT NOT NULL,size INTEGER NOT NULL,modified INTEGER NOT NULL,sha256 TEXT NOT NULL DEFAULT '',sha1 TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending',checkpoint TEXT NOT NULL DEFAULT '{}',PRIMARY KEY(batch,path))`,
		`CREATE INDEX IF NOT EXISTS pending_files ON files(batch,status,path)`,
		`CREATE TABLE IF NOT EXISTS publications(batch TEXT NOT NULL,key TEXT NOT NULL,body BLOB NOT NULL,ordinal INTEGER NOT NULL,status TEXT NOT NULL DEFAULT 'pending',PRIMARY KEY(batch,key))`,
		`INSERT OR IGNORE INTO settings(key,value) VALUES('source_uuid','` + randomID() + `')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			root.Close()
			return nil, err
		}
	}
	if err = os.Chmod(filepath.Join(options.Home, "transfers.sqlite"), 0600); err != nil {
		db.Close()
		root.Close()
		return nil, err
	}
	worker, cancel := context.WithCancel(ctx)
	m := &Manager{options: options, db: db, configs: configs, source: directorySource{root}, ctx: worker, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1), cleanupTried: map[string]time.Time{}}
	if err = db.QueryRow("SELECT value FROM settings WHERE key='source_uuid'").Scan(&m.sourceID); err != nil {
		cancel()
		db.Close()
		root.Close()
		return nil, err
	}
	_, err = db.Exec(`UPDATE batches SET status=CASE WHEN auto=1 THEN 'waiting_parked' ELSE 'paused' END,message='Interrupted; ready to resume when parked' WHERE status IN ('queued','scanning','running','finalizing')`)
	if err != nil {
		cancel()
		db.Close()
		root.Close()
		return nil, err
	}
	go m.run()
	return m, nil
}
func (m *Manager) Close() error      { m.cancel(); <-m.done; m.source.Close(); return m.db.Close() }
func (m *Manager) Configs() *Configs { return m.configs }
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) set(id, status, message string) error {
	statement := "UPDATE batches SET status=?,message=? WHERE id=?"
	if status == "scanning" || status == "running" || status == "finalizing" || status == "complete" || status == "complete_with_issues" {
		statement += " AND status NOT IN ('paused','canceling','canceled')"
	}
	_, err := m.db.Exec(statement, status, message, id)
	return err
}
func (m *Manager) Start(destination string, auto bool) (Batch, error) {
	if m.options.State() != "parked" {
		return Batch{}, errors.New("batch exports require confirmed parked state")
	}
	config, err := m.configs.Load(destination)
	if err != nil {
		return Batch{}, err
	}
	if (config.Kind == "gdrive" || config.Kind == "onedrive") && !config.Public().Connected {
		return Batch{}, ErrReconnect
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var active int
	if err = m.db.QueryRow(`SELECT count(*) FROM batches WHERE status NOT IN ('complete','complete_with_issues','canceled')`).Scan(&active); err != nil {
		return Batch{}, err
	}
	if active != 0 {
		return Batch{}, errors.New("finish or cancel the existing transfer first")
	}
	id := randomID()
	_, err = m.db.Exec("INSERT INTO batches(id,destination,fingerprint,status,message,created,auto) VALUES(?,?,?,'queued','Preparing inventory',?,?)", id, destination, fingerprint(config), time.Now().Unix(), auto)
	if err != nil {
		return Batch{}, err
	}
	m.signal()
	return m.batch(id)
}
func (m *Manager) Action(id, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := m.batch(id)
	if err != nil {
		return err
	}
	if terminal(b.Status) {
		return errors.New("this transfer has finished")
	}
	status, message := "", ""
	switch action {
	case "pause":
		status, message = "paused", "Paused by you"
	case "resume":
		if m.options.State() != "parked" {
			return errors.New("resume requires confirmed parked state")
		}
		status, message = "queued", "Resuming"
	case "cancel":
		status, message = "canceling", "Canceling; unfinished uploads will be cleaned while parked"
	default:
		return errors.New("unknown transfer action")
	}
	if err = m.set(id, status, message); err != nil {
		return err
	}
	if m.activeID == id && m.activeCancel != nil {
		m.activeCancel()
	}
	m.signal()
	return nil
}
func (m *Manager) batch(id string) (Batch, error) {
	var b Batch
	err := m.db.QueryRow(`SELECT id,destination,status,message,created,auto,cleanup FROM batches WHERE id=?`, id).Scan(&b.ID, &b.DestinationID, &b.Status, &b.Message, &b.Created, &b.AutoResume, &b.CleanupPending)
	if err != nil {
		return b, err
	}
	err = m.db.QueryRow(`SELECT count(*),coalesce(sum(size),0),coalesce(sum(CASE WHEN status='complete' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN status='complete' THEN size ELSE 0 END),0),coalesce(sum(CASE WHEN status IN ('missing','changed','conflict','deferred') THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN status='complete' THEN size ELSE coalesce(json_extract(checkpoint,'$.offset'),0) END),0) FROM files WHERE batch=?`, id).Scan(&b.TotalFiles, &b.TotalBytes, &b.CompletedFiles, &b.CompletedBytes, &b.Issues, &b.TransferredBytes)
	if err == nil {
		_ = m.db.QueryRow(`SELECT path FROM files WHERE batch=? AND status IN ('pending','copying') ORDER BY path LIMIT 1`, id).Scan(&b.CurrentFile)
	}
	return b, err
}
func (m *Manager) Snapshot() ([]Batch, error) {
	rows, err := m.db.Query("SELECT id FROM batches ORDER BY created DESC LIMIT 20")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []Batch{}
	for _, id := range ids {
		b, err := m.batch(id)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}
func (m *Manager) run() {
	defer close(m.done)
	for {
		if m.ctx.Err() != nil {
			return
		}
		worked := false
		if m.options.State() == "parked" {
			worked = m.next()
		} else {
			_, _ = m.db.Exec(`UPDATE batches SET status=CASE WHEN auto=1 THEN 'waiting_parked' ELSE 'paused' END,message='Paused until confirmed parked' WHERE status IN ('queued','scanning','running','finalizing')`)
		}
		delay := 250 * time.Millisecond
		if worked {
			delay = 5 * time.Millisecond
		}
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-time.After(delay):
		}
	}
}
func (m *Manager) next() bool {
	var id string
	if m.db.QueryRow(`SELECT id FROM batches WHERE status IN ('queued','scanning','running','finalizing','waiting_parked','canceling') ORDER BY created LIMIT 1`).Scan(&id) != nil {
		if m.db.QueryRow(`SELECT id FROM batches WHERE cleanup=1 AND status IN ('complete','complete_with_issues','canceled') ORDER BY created LIMIT 1`).Scan(&id) != nil || time.Since(m.cleanupTried[id]) < time.Minute {
			return false
		}
		m.cleanupTried[id] = time.Now()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	m.activeID, m.activeCancel = id, cancel
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if m.options.State() != "parked" {
					cancel()
					return
				}
			}
		}
	}()
	err := m.step(ctx, id)
	close(done)
	cancel()
	m.mu.Lock()
	m.activeID, m.activeCancel = "", nil
	b, readErr := m.batch(id)
	if readErr == nil && b.Status == "canceling" && err != nil && m.options.State() == "parked" && m.ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		_, _ = m.db.Exec("UPDATE batches SET status='canceled',message='Canceled; unfinished destination files will be cleaned when reachable',cleanup=1 WHERE id=?", id)
	}
	if readErr == nil && !terminal(b.Status) && b.Status != "paused" && b.Status != "canceling" && err != nil {
		if m.options.State() != "parked" || m.ctx.Err() != nil {
			if b.AutoResume {
				_ = m.set(id, "waiting_parked", "Paused until confirmed parked")
			} else {
				_ = m.set(id, "paused", "Interrupted; resume when parked")
			}
		} else if !errors.Is(err, context.Canceled) {
			_ = m.set(id, "needs_attention", safeError(err))
		}
	}
	m.mu.Unlock()
	return true
}
func safeError(err error) string {
	var remote RemoteError
	if errors.As(err, &remote) {
		return remote.Error() + "; check the destination and resume"
	}
	var destination DestinationError
	if errors.As(err, &destination) {
		return destination.Error()
	}
	switch {
	case errors.Is(err, ErrConflict):
		return ErrConflict.Error()
	case errors.Is(err, ErrMissing):
		return ErrMissing.Error()
	case errors.Is(err, ErrDisconnected):
		return ErrDisconnected.Error()
	case errors.Is(err, ErrReconnect):
		return ErrReconnect.Error()
	case errors.Is(err, ErrFull):
		return ErrFull.Error()
	default:
		return "Transfer interrupted. Check the destination and resume; completed files are preserved."
	}
}

var portableName = regexp.MustCompile(`^[a-z0-9_.-]+$`)
var portableReserved = regexp.MustCompile(`^(com|lpt)[1-9]$`)

func portable(name string) string {
	base := strings.Split(name, ".")[0]
	reserved := base == "con" || base == "prn" || base == "aux" || base == "nul" || portableReserved.MatchString(base)
	if len(name) <= 180 && portableName.MatchString(name) && !strings.HasSuffix(name, ".") && !reserved && name != "." && name != ".." {
		return name
	}
	if len(name) <= 89 {
		return "~" + hex.EncodeToString([]byte(name))
	}
	sum := sha256.Sum256([]byte(name))
	return "~h" + hex.EncodeToString(sum[:])
}
func (m *Manager) scan(ctx context.Context, id string) error {
	if err := m.set(id, "scanning", "Listing original recordings"); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.options.Root)
	if err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A failed/interrupted inventory transaction is discarded in full. Resuming
	// it creates a fresh snapshot before any destination writes are permitted.
	count := 0
	for _, folder := range entries {
		if err = ctx.Err(); err != nil {
			return err
		}
		match := SessionPattern.FindStringSubmatch(folder.Name())
		if !folder.IsDir() || len(match) != 3 {
			continue
		}
		directory, err := m.source.SafeDir.Open(folder.Name(), os.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return err
		}
		files, err := directory.ReadDir(-1)
		directory.Close()
		if err != nil {
			return err
		}
		locked := false
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".lock") {
				locked = true
			}
		}
		for _, file := range files {
			if file.Type()&os.ModeType != 0 || strings.HasSuffix(file.Name(), ".lock") {
				continue
			}
			count++
			if count > 100000 {
				return errors.New("archive inventory exceeds its file budget")
			}
			sourcePath := folder.Name() + "/" + file.Name()
			source, err := m.source.Open(sourcePath)
			if err != nil {
				return err
			}
			size, modified := source.Size, source.Modified.UnixNano()
			source.Close()
			status := "pending"
			if locked {
				status = "deferred"
			}
			key := path.Join("dash-archive/v1", m.sourceID, "sessions", portable(match[1]), "originals", portable(folder.Name()), portable(file.Name()))
			if _, err = tx.ExecContext(ctx, "INSERT INTO files(batch,path,key,session,size,modified,status) VALUES(?,?,?,?,?,?,?)", id, sourcePath, key, match[1], size, modified, status); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE batches SET scanned=1,status='running',message='Copying originals' WHERE id=? AND status NOT IN ('paused','canceling')", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) loadAsset(id string) (Asset, error) {
	var a Asset
	var raw string
	err := m.db.QueryRow(`SELECT path,key,session,size,modified,sha256,sha1,status,checkpoint FROM files WHERE batch=? AND status IN ('pending','copying') ORDER BY path LIMIT 1`, id).Scan(&a.Path, &a.Key, &a.Session, &a.Size, &a.Modified, &a.SHA256, &a.SHA1, &a.Status, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &a.Checkpoint)
	}
	return a, err
}
func (m *Manager) saveAsset(id string, a Asset) error {
	raw, err := json.Marshal(a.Checkpoint)
	if err != nil {
		return err
	}
	_, err = m.db.Exec("UPDATE files SET sha256=?,sha1=?,status=?,checkpoint=? WHERE batch=? AND path=?", a.SHA256, a.SHA1, a.Status, string(raw), id, a.Path)
	return err
}
func (m *Manager) saveIssue(id string, a Asset) error {
	if a.Checkpoint.UploadID != "" || a.Checkpoint.UploadURL != "" || a.Checkpoint.Offset > 0 {
		if _, err := m.db.Exec("UPDATE batches SET cleanup=1 WHERE id=?", id); err != nil {
			return err
		}
	}
	return m.saveAsset(id, a)
}
func (m *Manager) step(ctx context.Context, id string) error {
	b, err := m.batch(id)
	if err != nil {
		return err
	}
	var scanned bool
	var expected string
	if err = m.db.QueryRow("SELECT scanned,fingerprint FROM batches WHERE id=?", id).Scan(&scanned, &expected); err != nil {
		return err
	}
	config, err := m.configs.Load(b.DestinationID)
	if err != nil {
		return err
	}
	if fingerprint(config) != expected {
		return errors.New("destination configuration changed")
	}
	release, err := m.options.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if ctx.Err() != nil || m.options.State() != "parked" {
		return context.Canceled
	}
	if !scanned && b.Status != "canceling" && !b.CleanupPending {
		return m.scan(ctx, id)
	}
	store, err := m.options.Factory(ctx, config, m.source)
	if err != nil {
		return err
	}
	defer store.Close()
	if b.Status == "canceling" || (terminal(b.Status) && b.CleanupPending) {
		return m.cancelUploads(ctx, b, store)
	}
	a, err := m.loadAsset(id)
	if errors.Is(err, sql.ErrNoRows) {
		return m.finish(ctx, b, store)
	}
	if err != nil {
		return err
	}
	source, err := m.source.Open(a.Path)
	if err != nil {
		a.Status = "missing"
		return m.saveIssue(id, a)
	}
	if !unchanged(source, a) {
		source.Close()
		a.Status = "changed"
		return m.saveIssue(id, a)
	}
	if a.SHA256 == "" {
		a.SHA256, a.SHA1, err = Hash(ctx, source)
		source.Close()
		if err != nil {
			return err
		}
		return m.saveAsset(id, a)
	}
	source.Close()
	if err = m.set(id, "running", "Copying "+a.Path); err != nil {
		return err
	}
	checkpoint, done, err := store.Step(ctx, a)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			a.Status = "conflict"
			return m.saveIssue(id, a)
		}
		return err
	}
	a.Checkpoint = checkpoint
	if done {
		current, err := m.source.Open(a.Path)
		if err != nil {
			a.Status = "missing"
			return m.saveIssue(id, a)
		}
		same := unchanged(current, a)
		current.Close()
		if !same {
			a.Status = "changed"
			return m.saveIssue(id, a)
		}
	}
	a.Status = "copying"
	if done {
		a.Status = "complete"
		a.Checkpoint = Checkpoint{Offset: a.Size}
	}
	return m.saveAsset(id, a)
}

type Issue struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func (m *Manager) Issues(id string) ([]Issue, error) {
	rows, err := m.db.Query(`SELECT path,status FROM files WHERE batch=? AND status IN ('missing','changed','conflict','deferred') ORDER BY path LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Issue{}
	for rows.Next() {
		var issue Issue
		if err = rows.Scan(&issue.Path, &issue.Status); err != nil {
			return nil, err
		}
		result = append(result, issue)
	}
	return result, rows.Err()
}
func (m *Manager) DestinationBusy(id string) bool {
	var count int
	err := m.db.QueryRow(`SELECT count(*) FROM batches WHERE destination=? AND status NOT IN ('complete','complete_with_issues','canceled')`, id).Scan(&count)
	return err != nil || count > 0
}
func (m *Manager) WaitIdle(ctx context.Context) error {
	for {
		m.mu.Lock()
		active := m.activeID != ""
		m.mu.Unlock()
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (m *Manager) cancelUploads(ctx context.Context, b Batch, store Store) error {
	rows, err := m.db.Query("SELECT path,key,size,sha256,checkpoint FROM files WHERE batch=? AND status IN ('pending','copying','missing','changed','conflict') AND sha256!=''", b.ID)
	if err != nil {
		return err
	}
	assets := []Asset{}
	for rows.Next() {
		var a Asset
		var raw string
		if err = rows.Scan(&a.Path, &a.Key, &a.Size, &a.SHA256, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &a.Checkpoint); err != nil {
			rows.Close()
			return err
		}
		assets = append(assets, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range assets {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = store.Abort(ctx, a); err != nil {
			return err
		}
	}
	_, err = m.db.Exec("UPDATE batches SET cleanup=0 WHERE id=?", b.ID)
	if err != nil {
		return err
	}
	if terminal(b.Status) {
		return nil
	}
	return m.set(b.ID, "canceled", "Canceled; completed files were kept")
}
func (m *Manager) finish(ctx context.Context, b Batch, store Store) error {
	if err := m.set(b.ID, "finalizing", "Writing verified manifests"); err != nil {
		return err
	}
	var pendingKey string
	var pendingBody []byte
	err := m.db.QueryRow(`SELECT key,body FROM publications WHERE batch=? AND status='pending' ORDER BY ordinal LIMIT 1`, b.ID).Scan(&pendingKey, &pendingBody)
	if err == nil {
		if err = store.Put(ctx, pendingKey, pendingBody); err != nil {
			return err
		}
		_, err = m.db.Exec("UPDATE publications SET status='complete' WHERE batch=? AND key=?", b.ID, pendingKey)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var planned int
	if err = m.db.QueryRow("SELECT count(*) FROM publications WHERE batch=?", b.ID).Scan(&planned); err != nil {
		return err
	}
	if planned > 0 {
		status, message := "complete", "All originals verified"
		if b.Issues > 0 {
			status, message = "complete_with_issues", "Finished with missing, changing or conflicting originals; review issues"
		}
		return m.set(b.ID, status, message)
	}
	rows, err := m.db.Query("SELECT path,key,session,size,modified,sha256,status FROM files WHERE batch=? ORDER BY session,path", b.ID)
	if err != nil {
		return err
	}
	manifests := map[string]*archivepb.Manifest{}
	snapshot := &archivepb.Snapshot{Version: 1, SourceUuid: m.sourceID, SnapshotId: b.ID, RequestedUnixSeconds: b.Created}
	for rows.Next() {
		var a Asset
		if err = rows.Scan(&a.Path, &a.Key, &a.Session, &a.Size, &a.Modified, &a.SHA256, &a.Status); err != nil {
			rows.Close()
			return err
		}
		entry := &archivepb.Original{SourcePath: a.Path, ArchivePath: a.Key, Bytes: uint64(a.Size), Sha256: a.SHA256, ModifiedUnixNanoseconds: a.Modified, Status: a.Status}
		manifest := manifests[a.Session]
		if manifest == nil {
			manifest = &archivepb.Manifest{Version: 1, SourceUuid: m.sourceID, SessionId: a.Session, SnapshotId: b.ID, TimestampBasis: "Source identity uses native session ids; filesystem modification times are not capture times."}
			manifests[a.Session] = manifest
		}
		manifest.Originals = append(manifest.Originals, entry)
		snapshot.Files++
		snapshot.Bytes += uint64(a.Size)
		if a.Status != "complete" {
			snapshot.Issues = append(snapshot.Issues, entry)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// Snapshot-specific manifests are immutable: older verified archives remain
	// useful even when a later inventory includes changed or missing originals.
	base := path.Join("dash-archive/v1", m.sourceID, "snapshots", b.ID)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ordinal := 0
	publish := func(key string, data []byte) error {
		if len(data) > 16<<20 {
			return errors.New("archive manifest exceeds its memory budget")
		}
		ordinal++
		_, err := tx.ExecContext(ctx, "INSERT INTO publications(batch,key,body,ordinal) VALUES(?,?,?,?)", b.ID, key, data, ordinal)
		return err
	}
	schema, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{protodesc.ToFileDescriptorProto(archivepb.File_dash_archive_v1_archive_proto)}})
	if err != nil {
		return err
	}
	if err = publish(path.Join("dash-archive/v1", m.sourceID, "archive-schema.pb"), schema); err != nil {
		return err
	}
	if err = publish(path.Join("dash-archive/v1", m.sourceID, "README.txt"), []byte("Dash original recording archive v1. Original camera/audio/log bytes are unchanged. Protobuf descriptors are in archive-schema.pb. Each immutable snapshots/<id>/result.pb records the inventory; per-session manifests and SHA256SUMS are beside it. COMPLETE means every inventoried file verified; COMPLETE_WITH_ISSUES requires reviewing the result. Filesystem modification dates are not capture times.\n")); err != nil {
		return err
	}
	for session, manifest := range manifests {
		data, err := proto.Marshal(manifest)
		if err != nil {
			return err
		}
		if err = publish(path.Join(base, "sessions", portable(session), "manifest.pb"), data); err != nil {
			return err
		}
		var sums strings.Builder
		for _, entry := range manifest.Originals {
			if entry.Status == "complete" {
				fmt.Fprintf(&sums, "%s  %s\n", entry.Sha256, entry.ArchivePath)
			}
		}
		if err = publish(path.Join(base, "sessions", portable(session), "SHA256SUMS"), []byte(sums.String())); err != nil {
			return err
		}
	}
	data, err := proto.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err = publish(path.Join(base, "result.pb"), data); err != nil {
		return err
	}
	marker := "COMPLETE"
	if len(snapshot.Issues) > 0 {
		marker = "COMPLETE_WITH_ISSUES"
	}
	if err = publish(path.Join(base, marker), []byte("dash.archive.v1\n")); err != nil {
		return err
	}
	return tx.Commit()
}
