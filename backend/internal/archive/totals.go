package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Durable counters make status reads and per-chunk checkpoints constant-time.
// A legacy inventory is aggregated once, under the parked compute gate.
var totalColumns = []string{"files", "bytes", "complete_files", "complete_bytes", "progress_bytes", "issues", "issue_bytes", "sent_bytes", "reused_bytes"}

func fileTotals(prefix string) []string {
	p := func(name string) string { return prefix + name }
	return []string{"1", p("size"), "CASE WHEN " + p("status") + "='complete' THEN 1 ELSE 0 END", "CASE WHEN " + p("status") + "='complete' THEN " + p("size") + " ELSE 0 END", "CASE WHEN " + p("status") + "='complete' THEN " + p("size") + " ELSE coalesce(json_extract(" + p("checkpoint") + ",'$.offset'),0) END", "CASE WHEN " + p("status") + " IN ('missing','changed','conflict','deferred') THEN 1 ELSE 0 END", "CASE WHEN " + p("status") + " IN ('missing','changed','conflict','deferred') THEN " + p("size") + " ELSE 0 END", "coalesce(json_extract(" + p("checkpoint") + ",'$.sentBytes'),0)", "CASE WHEN " + p("status") + "='complete' AND json_extract(" + p("checkpoint") + ",'$.reused')=1 THEN " + p("size") + " ELSE 0 END"}
}
func installTotals(db *sql.DB) error {
	columns := []string{"batch TEXT PRIMARY KEY"}
	for _, name := range totalColumns {
		columns = append(columns, name+" INTEGER NOT NULL DEFAULT 0")
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS batch_totals(" + strings.Join(columns, ",") + ")"); err != nil {
		return err
	}
	columnsInfo, err := db.Query(`PRAGMA table_info(batches)`)
	if err != nil {
		return err
	}
	hasDirectory := false
	for columnsInfo.Next() {
		var id int
		var name, kind string
		var notNull, primary int
		var defaultValue any
		if err = columnsInfo.Scan(&id, &name, &kind, &notNull, &defaultValue, &primary); err != nil {
			columnsInfo.Close()
			return err
		}
		if name == "directory" {
			hasDirectory = true
		}
	}
	err = columnsInfo.Err()
	columnsInfo.Close()
	if err != nil {
		return err
	}
	if !hasDirectory {
		if _, err = db.Exec(`ALTER TABLE batches ADD COLUMN directory TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER IF NOT EXISTS archive_batch_insert AFTER INSERT ON batches BEGIN INSERT OR IGNORE INTO batch_totals(batch) VALUES(new.id); END`); err != nil {
		return err
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		next, old := fileTotals("new."), fileTotals("old.")
		updates := []string{}
		batch := "new.batch"
		if event == "DELETE" {
			batch = "old.batch"
		}
		for i, column := range totalColumns {
			delta := next[i]
			if event == "UPDATE" {
				delta = "(" + next[i] + ")-(" + old[i] + ")"
			}
			if event == "DELETE" {
				delta = "-(" + old[i] + ")"
			}
			updates = append(updates, column+"="+column+"+("+delta+")")
		}
		statement := fmt.Sprintf("CREATE TRIGGER IF NOT EXISTS archive_file_%s AFTER %s ON files BEGIN INSERT OR IGNORE INTO batch_totals(batch) VALUES(%s); UPDATE batch_totals SET %s WHERE batch=%s; END", strings.ToLower(event), event, batch, strings.Join(updates, ","), batch)
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) ensureTotals(parent context.Context) error {
	if m.totalsReady.Load() {
		return nil
	}
	m.totalsMu.Lock()
	defer m.totalsMu.Unlock()
	if m.totalsReady.Load() {
		return nil
	}
	ctx, cancel := parkedRequest(parent, m.options.State)
	defer cancel()
	release, err := m.options.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if ctx.Err() != nil || m.options.State() != "parked" {
		return context.Canceled
	}
	if _, err = m.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS archive_next_file ON files(batch,path) WHERE status IN ('pending','copying')`); err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	values := fileTotals("")
	for i := range values {
		values[i] = "coalesce(sum(" + values[i] + "),0)"
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO batch_totals(batch,"+strings.Join(totalColumns, ",")+") SELECT batch,"+strings.Join(values, ",")+" FROM files GROUP BY batch"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO batch_totals(batch) SELECT id FROM batches`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO settings(key,value) VALUES('summary_version','1')`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	m.totalsReady.Store(true)
	return nil
}
