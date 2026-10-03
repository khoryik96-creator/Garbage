package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

//go:embed schema.sql
var schema string

type Store struct{ DB *sql.DB }
type Repository struct{ Tx *sql.Tx }

func Open(path string) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, domain.Invalid("Use a file-backed database.")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := u.Query()
	q.Set("_busy_timeout", "10000")
	q.Set("_foreign_keys", "on")
	q.Set("_journal_mode", "WAL")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	err = s.Transaction(func(r *Repository) error {
		if _, err := r.Tx.Exec(schema); err != nil {
			return err
		}
		var version int
		if err := r.Tx.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
			return err
		}
		if version != 1 {
			return domain.Invalid("Unsupported database version.")
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Transaction(fn func(*Repository) error) error {
	return s.TransactionContext(context.Background(), fn)
}
func (s *Store) TransactionContext(ctx context.Context, fn func(*Repository) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(&Repository{Tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}
func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }
func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func found(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanCandidate(row scanner) (domain.Candidate, error) {
	var c domain.Candidate
	var other string
	err := row.Scan(&c.ID, &c.Name, &c.Country, &c.AddressCountry, &c.Notes, &other, &c.Version)
	if err != nil {
		return c, found(err)
	}
	err = json.Unmarshal([]byte(other), &c.OtherFields)
	return c, err
}

const candidateColumns = "id,name,country,address_country,notes,other_fields,version"

func (r *Repository) Candidate(id int) (domain.Candidate, error) {
	return scanCandidate(r.Tx.QueryRow("SELECT "+candidateColumns+" FROM demo_candidates WHERE id=?", id))
}
func (r *Repository) AddCandidate(c domain.Candidate) error {
	if c.OtherFields == nil {
		c.OtherFields = map[string]string{}
	}
	if c.Version == 0 {
		c.Version = 1
	}
	_, err := r.Tx.Exec("INSERT INTO demo_candidates("+candidateColumns+") VALUES(?,?,?,?,?,?,?)", c.ID, c.Name, c.Country, c.AddressCountry, c.Notes, encode(c.OtherFields), c.Version)
	return err
}
func (r *Repository) Page(after, through, limit int) (domain.CandidatePage, error) {
	p := domain.CandidatePage{Candidates: []domain.Candidate{}, Cursor: after}
	rows, err := r.Tx.Query("SELECT "+candidateColumns+" FROM demo_candidates WHERE id>? AND id<=? ORDER BY id LIMIT ?", after, through, limit)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return p, err
		}
		p.Candidates = append(p.Candidates, c)
		p.Cursor = c.ID
	}
	p.Finished = len(p.Candidates) < limit
	return p, rows.Err()
}
func (r *Repository) Counts() (domain.Counts, error) {
	var c domain.Counts
	rows, err := r.Tx.Query("SELECT id,country FROM demo_candidates")
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var country *string
		if err := rows.Scan(&id, &country); err != nil {
			return c, err
		}
		c.Total++
		if domain.Empty(country) {
			c.Missing++
		}
		if id > c.UpperBound {
			c.UpperBound = id
		}
	}
	c.Existing = c.Total - c.Missing
	return c, rows.Err()
}
func (r *Repository) MutateCountry(before domain.Candidate, value *string, expected *string) (domain.Mutation, error) {
	if expected == nil && !domain.Empty(before.Country) {
		return domain.Mutation{Candidate: before}, nil
	}
	guard := before.Country
	if expected != nil {
		guard = expected
	}
	result, err := r.Tx.Exec("UPDATE demo_candidates SET country=?,version=version+1 WHERE id=? AND version=? AND country IS ?", value, before.ID, before.Version, guard)
	if err != nil {
		return domain.Mutation{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return domain.Mutation{}, err
	}
	after, err := r.Candidate(before.ID)
	return domain.Mutation{Applied: n == 1, Candidate: after}, err
}
func (r *Repository) EditCountry(id int, value *string) (domain.Candidate, error) {
	if _, err := r.Candidate(id); err != nil {
		return domain.Candidate{}, err
	}
	if _, err := r.Tx.Exec("UPDATE demo_candidates SET country=?,version=version+1 WHERE id=?", value, id); err != nil {
		return domain.Candidate{}, err
	}
	return r.Candidate(id)
}

const runColumns = "id,mode,fields,state,total,processed,missing,proposed,existing,not_found,cursor,upper_bound,created_at,error"

func scanRun(row scanner) (domain.Run, error) {
	var r domain.Run
	var fields string
	err := row.Scan(&r.ID, &r.Mode, &fields, &r.State, &r.Total, &r.Processed, &r.Missing, &r.Proposed, &r.Existing, &r.NotFound, &r.Cursor, &r.UpperBound, &r.CreatedAt, &r.Error)
	if err != nil {
		return r, found(err)
	}
	return r, json.Unmarshal([]byte(fields), &r.Fields)
}
func (r *Repository) Run(id string) (domain.Run, error) {
	return scanRun(r.Tx.QueryRow("SELECT "+runColumns+" FROM runs WHERE id=?", id))
}
func (r *Repository) NewRun(request domain.RunRequest) (domain.Run, error) {
	if err := request.Validate(); err != nil {
		return domain.Run{}, err
	}
	counts, err := r.Counts()
	if err != nil {
		return domain.Run{}, err
	}
	id := domain.ID()
	_, err = r.Tx.Exec("INSERT INTO runs("+runColumns+") VALUES(?,?,?,'queued',?,0,0,0,0,0,0,?,?,NULL)", id, request.Mode, encode(request.Fields), counts.Total, counts.UpperBound, Now())
	if err != nil {
		return domain.Run{}, err
	}
	_, err = r.Tx.Exec("INSERT INTO jobs(id,state,attempts,available_at) VALUES(?,'queued',0,0)", id)
	if err != nil {
		return domain.Run{}, err
	}
	if err = r.Audit("run_created", &id, nil, map[string]any{"mode": request.Mode, "fields": request.Fields}); err != nil {
		return domain.Run{}, err
	}
	return r.Run(id)
}
func (r *Repository) Runs() (out []domain.Run, err error) {
	out = []domain.Run{}
	rows, err := r.Tx.Query("SELECT " + runColumns + " FROM runs ORDER BY created_at DESC LIMIT 30")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanRun(rows)
		if e != nil {
			return out, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *Repository) ChangeRun(id, action string) (domain.Run, error) {
	run, err := r.Run(id)
	if err != nil {
		return run, err
	}
	state := ""
	switch action {
	case "pause":
		if run.State == "queued" || run.State == "running" {
			state = "paused"
		}
	case "resume":
		if run.State == "paused" || run.State == "failed" {
			state = "queued"
		}
	case "cancel":
		if run.State == "queued" || run.State == "running" || run.State == "paused" || run.State == "failed" {
			state = "cancelled"
		}
	}
	if state == "" {
		return run, domain.ErrConflict
	}
	if err = changed(r.Tx.Exec("UPDATE runs SET state=?,error=NULL WHERE id=? AND state=?", state, id, run.State)); err != nil {
		return run, err
	}
	if action == "pause" {
		_, err = r.Tx.Exec("UPDATE jobs SET state='queued',token=NULL,lease_until=NULL WHERE id=?", id)
	} else if action == "resume" {
		_, err = r.Tx.Exec("UPDATE jobs SET state='queued',token=NULL,lease_until=NULL,available_at=0,attempts=0,error=NULL WHERE id=?", id)
	} else if action == "cancel" {
		_, err = r.Tx.Exec("UPDATE jobs SET state='done',token=NULL,lease_until=NULL WHERE id=?", id)
	}
	if err != nil {
		return run, err
	}
	if err = r.Audit("run_"+action, &id, nil, nil); err != nil {
		return run, err
	}
	return r.Run(id)
}
func (r *Repository) AddSuggestion(run domain.Run, c domain.Candidate, e domain.Extraction) error {
	state := "pending"
	if run.Mode == "preview" {
		state = "preview"
	}
	_, err := r.Tx.Exec("INSERT INTO suggestions(id,run_id,candidate_id,field,value,confidence,evidence,reason,state) VALUES(?,?,?,'country',?,?,?,?,?)", domain.ID(), run.ID, c.ID, e.Value, e.Confidence, encode(e.Evidence), e.Reason, state)
	return err
}

const suggestionColumns = "s.id,s.run_id,s.candidate_id,c.name,s.field,s.value,s.confidence,s.evidence,s.reason,s.state"

func scanSuggestion(row scanner) (domain.Suggestion, error) {
	var s domain.Suggestion
	var evidence string
	err := row.Scan(&s.ID, &s.RunID, &s.CandidateID, &s.CandidateName, &s.Field, &s.Value, &s.Confidence, &evidence, &s.Reason, &s.State)
	if err != nil {
		return s, found(err)
	}
	return s, json.Unmarshal([]byte(evidence), &s.Evidence)
}
func (r *Repository) Suggestion(id string) (domain.Suggestion, error) {
	return scanSuggestion(r.Tx.QueryRow("SELECT "+suggestionColumns+" FROM suggestions s JOIN demo_candidates c ON c.id=s.candidate_id WHERE s.id=?", id))
}
func (r *Repository) Suggestions(id, after string, limit int) ([]domain.Suggestion, error) {
	out := []domain.Suggestion{}
	rows, err := r.Tx.Query("SELECT "+suggestionColumns+" FROM suggestions s JOIN demo_candidates c ON c.id=s.candidate_id WHERE s.run_id=? AND s.id>? ORDER BY s.id LIMIT ?", id, after, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		s, e := scanSuggestion(rows)
		if e != nil {
			return out, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *Repository) SuggestionState(id, expected, state string) error {
	return changed(r.Tx.Exec("UPDATE suggestions SET state=? WHERE id=? AND state=?", state, id, expected))
}

const writeColumns = "id,suggestion_id,run_id,candidate_id,before_value,after_value,after_version,state,created_at"

func scanWrite(row scanner) (domain.Writeback, error) {
	var w domain.Writeback
	err := row.Scan(&w.ID, &w.SuggestionID, &w.RunID, &w.CandidateID, &w.BeforeValue, &w.AfterValue, &w.AfterVersion, &w.State, &w.CreatedAt)
	return w, found(err)
}
func (r *Repository) RecordWrite(s domain.Suggestion, before, after domain.Candidate) (string, error) {
	id := domain.ID()
	_, err := r.Tx.Exec("INSERT INTO writebacks("+writeColumns+") VALUES(?,?,?,?,?,?,?,'applied',?)", id, s.ID, s.RunID, s.CandidateID, before.Country, after.Country, after.Version, Now())
	return id, err
}
func (r *Repository) Write(id string) (domain.Writeback, error) {
	return scanWrite(r.Tx.QueryRow("SELECT "+writeColumns+" FROM writebacks WHERE id=?", id))
}
func (r *Repository) ReserveUndo(id string) error {
	return changed(r.Tx.Exec("UPDATE writebacks SET state='undoing' WHERE id=? AND state='applied'", id))
}
func (r *Repository) WriteState(id, state string) error {
	return changed(r.Tx.Exec("UPDATE writebacks SET state=? WHERE id=? AND state='undoing'", state, id))
}
func (r *Repository) Writes(id string) ([]domain.Writeback, error) {
	out := []domain.Writeback{}
	rows, err := r.Tx.Query("SELECT "+writeColumns+" FROM writebacks WHERE run_id=? ORDER BY created_at DESC LIMIT 50", id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		w, e := scanWrite(rows)
		if e != nil {
			return out, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
func (r *Repository) Audit(action string, runID *string, candidateID *int, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	_, err := r.Tx.Exec("INSERT INTO audit_events(id,run_id,candidate_id,action,details,created_at) VALUES(?,?,?,?,?,?)", domain.ID(), runID, candidateID, action, encode(details), Now())
	return err
}
func (r *Repository) Audits(before float64, beforeID string, limit int) ([]domain.AuditEvent, error) {
	out := []domain.AuditEvent{}
	rows, err := r.Tx.Query("SELECT id,run_id,candidate_id,action,details,created_at FROM audit_events WHERE created_at<? OR (created_at=? AND id<?) ORDER BY created_at DESC,id DESC LIMIT ?", before, before, beforeID, limit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.AuditEvent
		var details string
		if err = rows.Scan(&a.ID, &a.RunID, &a.CandidateID, &a.Action, &details, &a.CreatedAt); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(details), &a.Details); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
