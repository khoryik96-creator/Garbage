package integration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/audit"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/storage"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

func TestRestoreCopyFailureRecoversPreviousWorkspace(t *testing.T) {
	s, w, database := setup(t)
	run := completed(t, s, w, "review")
	backup := filepath.Join(t.TempDir(), "backup.db")
	must(t, s.Backup(context.Background(), backup))
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.EditCountry(1001, domain.String("GB")); return err }))
	// Hold a different connection's writer lock through the restore deadline.
	// The recovery snapshot can read the committed WAL state, but replacing the
	// destination must fail and then recover after this writer releases its lock.
	_, err := s.DB.Exec("PRAGMA busy_timeout=1")
	must(t, err)
	external, err := sql.Open("sqlite3", database+"?_txlock=immediate&_journal_mode=WAL")
	must(t, err)
	defer external.Close()
	tx, err := external.Begin()
	must(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("UPDATE demo_candidates SET country='NZ' WHERE id=1001")
	must(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	released := make(chan error, 1)
	go func() { <-ctx.Done(); time.Sleep(50 * time.Millisecond); released <- tx.Rollback() }()
	recovery, err := s.Restore(ctx, backup)
	if err == nil || recovery == "" {
		t.Fatal("copy failure did not retain a recovery snapshot", err)
	}
	must(t, <-released)
	if value := candidate(t, s, 1001).Country; value == nil || *value != "GB" {
		t.Fatal("failed restore lost the previous committed value")
	}
	if getRun(t, s, run.ID).Proposed != 6 {
		t.Fatal("failed restore lost history")
	}
}

func TestOnlineBackupRestoreRetainsApprovalAuditAndUndo(t *testing.T) {
	s, w, database := setup(t)
	run := completed(t, s, w, "review")
	suggestion := proposal(t, s, run.ID, 1001)
	review := audit.New(s)
	approved, err := review.Approve(suggestion.ID, domain.Approval{})
	must(t, err)
	backup := filepath.Join(t.TempDir(), "backup.db")
	must(t, s.Backup(context.Background(), backup))
	// Changes committed while WAL is active must be included in the backup.
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.EditCountry(1001, domain.String("NZ")); return err }))
	recovery, err := s.Restore(context.Background(), backup)
	must(t, err)
	if recovery == "" || candidate(t, s, 1001).Country == nil || *candidate(t, s, 1001).Country != "AU" {
		t.Fatal("restore lost a committed approval")
	}
	if getRun(t, s, run.ID).Proposed != 6 {
		t.Fatal("restore lost completed run history")
	}
	state, err := review.Undo(*approved.WritebackID)
	must(t, err)
	if state != "undone" || !domain.Empty(candidate(t, s, 1001).Country) {
		t.Fatal("restored approval could not be undone")
	}
	previous, err := storage.Open(filepath.Join(filepath.Dir(database), recovery))
	must(t, err)
	defer previous.Close()
	if value := candidate(t, previous, 1001).Country; value == nil || *value != "NZ" {
		t.Fatal("recovery backup lost the previous workspace")
	}
}

func TestRestoreFencesInFlightClaimsAndPausesActiveRuns(t *testing.T) {
	s, w, _ := setup(t)
	run := newRun(t, s, "review")
	_, err := w.ProcessOne()
	must(t, err)
	var claim *domain.Claim
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		claim, err = r.Claim(storage.Now(), 60, 5)
		return err
	}))
	backup := filepath.Join(t.TempDir(), "active.db")
	must(t, s.Backup(context.Background(), backup))
	_, err = s.Restore(context.Background(), backup)
	must(t, err)
	if current := getRun(t, s, run.ID); current.State != "paused" || current.Processed != 3 {
		t.Fatalf("active checkpoint was not retained and paused: %+v", current)
	}
	if err = w.ProcessClaim(*claim); !errors.Is(err, domain.ErrLease) {
		t.Fatal("stale claim survived restore", err)
	}
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, "resume"); return err }))
	drain(t, w)
	if current := getRun(t, s, run.ID); current.Processed != 10 || current.Proposed != 6 {
		t.Fatal("resuming restored run duplicated or lost work", current)
	}
}

func TestInvalidRestoreAndCancelledBackupKeepCurrentData(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "preview")
	for _, kind := range []string{"corrupt", "empty", "version", "trigger", "orphan"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.db")
			if kind == "corrupt" {
				must(t, os.WriteFile(path, []byte("damaged data"), 0600))
			} else if kind == "empty" {
				db, err := sql.Open("sqlite3", path)
				must(t, err)
				_, err = db.Exec("CREATE TABLE irrelevant(id INTEGER)")
				must(t, err)
				must(t, db.Close())
			} else {
				must(t, s.Backup(context.Background(), path))
				db, err := sql.Open("sqlite3", path)
				must(t, err)
				query := map[string]string{"version": "UPDATE schema_migrations SET version=99", "trigger": "CREATE TRIGGER unsafe AFTER INSERT ON runs BEGIN DELETE FROM demo_candidates; END", "orphan": "UPDATE suggestions SET candidate_id=candidate_id+999999"}[kind]
				_, err = db.Exec(query)
				must(t, err)
				must(t, db.Close())
			}
			if _, err := s.Restore(context.Background(), path); err == nil {
				t.Fatal("invalid backup accepted")
			}
			if getRun(t, s, run.ID).Proposed != 6 {
				t.Fatal("failed restore changed saved work")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "cancelled.db")
	if err := s.Backup(ctx, path); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("incomplete backup was left behind")
	}
}

func TestBackupAndMultipartRestoreRequireCSRF(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "preview")
	handler, err := web.New(s, false)
	must(t, err)
	b := browser{handler: handler, token: domain.ID()}
	if b.request("POST", "/workspace/backup", "", false).Code != 403 {
		t.Fatal("backup bypassed CSRF")
	}
	download := b.request("POST", "/workspace/backup", "", true)
	if download.Code != 200 || !bytes.HasPrefix(download.Body.Bytes(), []byte("SQLite format 3")) {
		t.Fatal("backup download invalid", download.Body.String())
	}
	for _, csrf := range []bool{false, true} {
		body := new(bytes.Buffer)
		form := multipart.NewWriter(body)
		if csrf {
			must(t, form.WriteField("_csrf", b.token))
		}
		file, err := form.CreateFormFile("backup", "backup.db")
		must(t, err)
		_, err = io.Copy(file, bytes.NewReader(download.Body.Bytes()))
		must(t, err)
		must(t, form.Close())
		request := httptest.NewRequest("POST", "http://localhost/workspace/restore", body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		request.AddCookie(&http.Cookie{Name: "gt_csrf", Value: b.token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !csrf && response.Code != 403 {
			t.Fatal("restore bypassed CSRF")
		}
		if csrf && response.Code != 303 {
			t.Fatal("multipart restore failed", response.Code, response.Body.String())
		}
	}
	if getRun(t, s, run.ID).Processed != 10 {
		t.Fatal("HTTP restore lost run history")
	}
}

func TestTypedMalformedRestoreRejectedBeforeReplacingWorkspace(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	handler, err := web.New(s, false)
	must(t, err)
	b := browser{handler: handler, token: domain.ID()}
	for _, query := range []string{
		`UPDATE demo_candidates SET other_fields='{"department":123}' WHERE id=1001`,
		`UPDATE demo_candidates SET other_fields='{"department":null}' WHERE id=1001`,
		`UPDATE suggestions SET evidence='[42]'`,
		`UPDATE suggestions SET evidence='[{"source":42,"quote":"text"}]'`,
		`UPDATE runs SET created_at='not-a-timestamp'`,
		`INSERT INTO audit_events VALUES('invalid-audit',NULL,'not-an-integer','test','{}',1)`,
		`UPDATE jobs SET available_at='not-a-number'`,
	} {
		t.Run(query, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.db")
			must(t, s.Backup(context.Background(), path))
			db, err := sql.Open("sqlite3", path)
			must(t, err)
			_, err = db.Exec(query)
			must(t, err)
			must(t, db.Close())
			body := new(bytes.Buffer)
			form := multipart.NewWriter(body)
			must(t, form.WriteField("_csrf", b.token))
			file, err := form.CreateFormFile("backup", "backup.db")
			must(t, err)
			data, err := os.ReadFile(path)
			must(t, err)
			_, err = file.Write(data)
			must(t, err)
			must(t, form.Close())
			request := httptest.NewRequest("POST", "http://localhost/workspace/restore", body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			request.AddCookie(&http.Cookie{Name: "gt_csrf", Value: b.token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 422 {
				t.Fatalf("malformed backup reported success: %d %s", response.Code, response.Body.String())
			}
			if response := b.request("GET", "/profiles", "", false); response.Code != 200 {
				t.Fatal("original profiles became unreadable", response.Code)
			}
			if response := b.request("GET", "/audit", "", false); response.Code != 200 {
				t.Fatal("original audit became unreadable", response.Code)
			}
			if getRun(t, s, run.ID).Proposed != 6 {
				t.Fatal("rejected restore changed history")
			}
		})
	}
}
