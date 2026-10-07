package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/mattn/go-sqlite3"
)

func backupDB(path string, readOnly bool) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("_busy_timeout", "10000")
	q.Set("_foreign_keys", "on")
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rw")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

// SQLite's online backup API includes committed WAL pages and creates a single
// consistent snapshot. Finishing an incomplete copy rolls back its destination.
func copySQLite(ctx context.Context, destination, source *sql.DB) error {
	src, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := destination.Conn(ctx)
	if err != nil {
		return err
	}
	defer dst.Close()
	return src.Raw(func(source any) error {
		return dst.Raw(func(destination any) error {
			backup, err := destination.(*sqlite3.SQLiteConn).Backup("main", source.(*sqlite3.SQLiteConn), "main")
			if err != nil {
				return err
			}
			for {
				if err = ctx.Err(); err != nil {
					return errors.Join(err, backup.Finish())
				}
				var done bool
				done, err = backup.Step(128)
				if err != nil || done {
					return errors.Join(err, backup.Finish())
				}
				select {
				case <-ctx.Done():
					return errors.Join(ctx.Err(), backup.Finish())
				case <-time.After(time.Millisecond):
				}
			}
		})
	})
}

func (s *Store) backup(ctx context.Context, path string) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	db, err := backupDB(path, false)
	if err != nil {
		return err
	}
	err = copySQLite(ctx, db, s.DB)
	err = errors.Join(err, db.Close())
	if err != nil {
		return err
	}
	file, err = os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = file.Sync()
	return errors.Join(err, file.Close())
}

func (s *Store) Backup(ctx context.Context, path string) error {
	s.maintenance.RLock()
	defer s.maintenance.RUnlock()
	return s.backup(ctx, path)
}

// Validate without opening through Open: migrations must never turn an arbitrary
// SQLite database into a seemingly valid workspace. No uploaded SQL is executed.
func validateBackup(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return domain.Invalid("The backup is damaged or is not a SQLite database.")
	}
	var version, count int
	if err := db.QueryRowContext(ctx, "SELECT MAX(version),COUNT(*) FROM schema_migrations").Scan(&version, &count); err != nil || (version != 1 && version != 2) || count != version {
		return domain.Invalid("This backup has an unsupported workspace version.")
	}
	var unsafe int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type IN ('trigger','view') OR (type='table' AND name NOT IN ('schema_migrations','demo_candidates','runs','jobs','suggestions','writebacks','audit_events'))").Scan(&unsafe); err != nil || unsafe != 0 {
		return domain.Invalid("The backup contains an unsupported database schema.")
	}
	runFields, writeFields := runColumns, writeColumns
	if version == 1 {
		runFields, writeFields = legacyRunColumns, legacyWriteColumns
	}
	projections := []string{
		"SELECT " + candidateColumns + " FROM demo_candidates LIMIT 0",
		"SELECT " + runFields + " FROM runs LIMIT 0",
		"SELECT id,state,attempts,available_at,lease_until,token,error FROM jobs LIMIT 0",
		"SELECT id,run_id,candidate_id,field,value,confidence,evidence,reason,state FROM suggestions LIMIT 0",
		"SELECT " + writeFields + " FROM writebacks LIMIT 0",
		"SELECT id,run_id,candidate_id,action,details,created_at FROM audit_events LIMIT 0",
	}
	for _, query := range projections {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return domain.Invalid("The backup is missing required workspace tables or fields.")
		}
		rows.Close()
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil || invalid {
		return domain.Invalid("The backup contains inconsistent saved records.")
	}
	checks := []string{
		"SELECT COUNT(*) FROM demo_candidates WHERE NOT json_valid(other_fields) OR json_type(other_fields)!='object' OR version<1",
		"SELECT COUNT(*) FROM demo_candidates,json_each(other_fields) WHERE json_each.type!='text'",
		"SELECT COUNT(*) FROM runs WHERE NOT json_valid(fields) OR json_type(fields)!='array' OR json_array_length(fields)<1 OR json_array_length(fields)>20 OR mode NOT IN ('preview','review') OR state NOT IN ('queued','running','completed','paused','failed','cancelled') OR processed<0 OR processed>total",
		"SELECT COUNT(*) FROM suggestions WHERE NOT json_valid(evidence) OR json_type(evidence)!='array' OR run_id NOT IN (SELECT id FROM runs) OR candidate_id NOT IN (SELECT id FROM demo_candidates)",
		"SELECT COUNT(*) FROM suggestions,json_each(evidence) WHERE json_each.type!='object' OR json_type(json_each.value,'$.source') IS NOT 'text' OR json_type(json_each.value,'$.quote') IS NOT 'text'",
		"SELECT COUNT(*) FROM writebacks WHERE suggestion_id NOT IN (SELECT id FROM suggestions) OR run_id NOT IN (SELECT id FROM runs) OR candidate_id NOT IN (SELECT id FROM demo_candidates)",
		"SELECT COUNT(*) FROM jobs WHERE state NOT IN ('queued','leased','done','failed') OR attempts<0 OR id NOT IN (SELECT id FROM runs)",
		"SELECT COUNT(*) FROM audit_events WHERE NOT json_valid(details) OR json_type(details)!='object'",
		"SELECT COUNT(*) FROM runs WHERE id NOT IN (SELECT id FROM jobs)",
	}
	for _, query := range checks {
		if err := db.QueryRowContext(ctx, query).Scan(&unsafe); err != nil || unsafe != 0 {
			return domain.Invalid("The backup contains inconsistent saved records.")
		}
	}
	// Exercise the same typed decoders as ordinary pages and services. SQLite
	// integrity and valid JSON alone do not imply readable application records.
	readers := []struct {
		query string
		read  func(scanner) error
	}{
		{"SELECT " + candidateColumns + " FROM demo_candidates", func(row scanner) error { _, err := scanCandidate(row); return err }},
		{"SELECT " + runFields + " FROM runs", func(row scanner) error {
			var run domain.Run
			var raw string
			args := []any{&run.ID, &run.Mode, &raw, &run.State, &run.Total, &run.Processed, &run.Missing, &run.Proposed, &run.Existing, &run.NotFound, &run.Cursor, &run.UpperBound, &run.CreatedAt, &run.Error}
			if version == 2 {
				args = append(args, &run.Protection)
			}
			if err := row.Scan(args...); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &run.Fields); err != nil {
				return err
			}
			return (domain.RunRequest{Fields: run.Fields, Mode: run.Mode, Protection: run.Protection}).Validate()
		}},
		{"SELECT " + suggestionColumns + " FROM suggestions s JOIN demo_candidates c ON c.id=s.candidate_id", func(row scanner) error {
			s, err := scanSuggestion(row)
			if err == nil && !domain.RunField(s.Field) {
				return domain.Invalid("Unsupported proposal field.")
			}
			return err
		}},
		{"SELECT " + writeFields + " FROM writebacks", func(row scanner) error {
			var w domain.Writeback
			args := []any{&w.ID, &w.SuggestionID, &w.RunID, &w.CandidateID, &w.BeforeValue, &w.AfterValue, &w.AfterVersion, &w.State, &w.CreatedAt}
			if version == 2 {
				args = append(args, &w.Field, &w.GuardVersion)
			}
			if err := row.Scan(args...); err != nil {
				return err
			}
			if version == 2 && !domain.RunField(w.Field) {
				return domain.Invalid("Unsupported writeback field.")
			}
			return nil
		}},
		{"SELECT id,state,attempts,available_at,lease_until,token,error FROM jobs", func(row scanner) error {
			var id, state string
			var attempts int
			var available float64
			var lease *float64
			var token, failure *string
			return row.Scan(&id, &state, &attempts, &available, &lease, &token, &failure)
		}},
		{"SELECT id,run_id,candidate_id,action,details,created_at FROM audit_events", func(row scanner) error {
			var event domain.AuditEvent
			var details string
			return row.Scan(&event.ID, &event.RunID, &event.CandidateID, &event.Action, &details, &event.CreatedAt)
		}},
	}
	for _, reader := range readers {
		rows, err := db.QueryContext(ctx, reader.query)
		if err != nil {
			return domain.Invalid("The backup contains unreadable saved records.")
		}
		for rows.Next() {
			if err = reader.read(rows); err != nil {
				rows.Close()
				return domain.Invalid("The backup contains unreadable saved records.")
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return domain.Invalid("The backup contains unreadable saved records.")
		}
	}
	return nil
}

// Restore copies a validated, prepared snapshot atomically into the open SQLite
// connection. It fences outstanding claims and preserves a recovery snapshot.
func (s *Store) Restore(ctx context.Context, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || abs == s.Path {
		return "", domain.Invalid("Choose a separate backup file to restore.")
	}
	s.maintenance.Lock()
	defer s.maintenance.Unlock()
	source, err := backupDB(path, false)
	if err != nil {
		return "", err
	}
	defer source.Close()
	if err = validateBackup(ctx, source); err != nil {
		return "", err
	}
	stage, err := os.CreateTemp(filepath.Dir(s.Path), ".restore-prepared-*.db")
	if err != nil {
		return "", err
	}
	stagePath := stage.Name()
	if err = stage.Close(); err != nil {
		return "", err
	}
	defer os.Remove(stagePath)
	prepared, err := backupDB(stagePath, false)
	if err != nil {
		return "", err
	}
	defer prepared.Close()
	if err = copySQLite(ctx, prepared, source); err != nil {
		return "", err
	}
	source = prepared
	tx, err := source.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		return "", err
	}
	if version == 1 {
		if _, err = tx.ExecContext(ctx, migration2); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET state='paused',error=NULL WHERE state IN ('queued','running'); UPDATE jobs SET state='queued',token=NULL,lease_until=NULL,available_at=0,attempts=0,error=NULL WHERE id IN (SELECT id FROM runs WHERE state='paused')"); err != nil {
		return "", err
	}
	if err = (&Repository{Tx: tx}).Audit("workspace_restored", nil, nil, map[string]any{"active_runs": "paused; resume when ready"}); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	recovery := filepath.Join(filepath.Dir(s.Path), "before-restore-"+domain.ID()+".db")
	if err = s.backup(ctx, recovery); err != nil {
		return "", domain.Invalid("A recovery backup could not be saved. Your current workspace has been kept.")
	}
	if err = copySQLite(ctx, s.DB, source); err != nil {
		// An incomplete backup rolls back, but also recover explicitly if finalizing
		// the copy reported an error. Do not admit further writes if recovery fails.
		recoverCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		original, openErr := backupDB(recovery, true)
		if openErr == nil {
			openErr = copySQLite(recoverCtx, s.DB, original)
			_ = original.Close()
		}
		if openErr != nil {
			s.restoreFailed = true
			return filepath.Base(recovery), fmt.Errorf("restore and recovery failed; recovery snapshot: %s", filepath.Base(recovery))
		}
		return filepath.Base(recovery), domain.Invalid("Restore failed. Your previous workspace was recovered; its backup is kept in the workspace folder.")
	}
	s.restoreFailed = false
	return filepath.Base(recovery), nil
}
