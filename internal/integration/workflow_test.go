package integration

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/audit"
	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/jobs"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

func setup(t *testing.T) (*storage.Store, *jobs.Worker, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo.db")
	s, err := storage.Open(path)
	must(t, err)
	t.Cleanup(func() { s.Close() })
	must(t, s.Transaction(connectors.Seed))
	w := jobs.New(s, 3)
	return s, w, path
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func newRun(t *testing.T, s *storage.Store, mode string) domain.Run {
	t.Helper()
	var run domain.Run
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		run, err = r.NewRun(domain.RunRequest{Mode: mode, Fields: []string{"country"}})
		return err
	}))
	return run
}
func getRun(t *testing.T, s *storage.Store, id string) domain.Run {
	t.Helper()
	var run domain.Run
	must(t, s.Transaction(func(r *storage.Repository) error { var err error; run, err = r.Run(id); return err }))
	return run
}
func drain(t *testing.T, w *jobs.Worker) {
	t.Helper()
	for i := 0; i < 100; i++ {
		processed, err := w.ProcessOne()
		must(t, err)
		if !processed {
			return
		}
	}
	t.Fatal("worker did not settle")
}
func completed(t *testing.T, s *storage.Store, w *jobs.Worker, mode string) domain.Run {
	t.Helper()
	run := newRun(t, s, mode)
	drain(t, w)
	return getRun(t, s, run.ID)
}
func proposal(t *testing.T, s *storage.Store, id string, candidate int) domain.Suggestion {
	t.Helper()
	var result domain.Suggestion
	must(t, s.Transaction(func(r *storage.Repository) error {
		items, err := r.Suggestions(id, "", 100)
		for _, s := range items {
			if s.CandidateID == candidate {
				result = s
			}
		}
		return err
	}))
	if result.ID == "" {
		t.Fatal("proposal missing")
	}
	return result
}
func candidate(t *testing.T, s *storage.Store, id int) domain.Candidate {
	t.Helper()
	var c domain.Candidate
	must(t, s.Transaction(func(r *storage.Repository) error { var err error; c, err = r.Candidate(id); return err }))
	return c
}
func TestPreviewPreservesProfilesAndReportsGaps(t *testing.T) {
	s, w, _ := setup(t)
	before := candidate(t, s, 1001)
	run := completed(t, s, w, "preview")
	if run.State != "completed" || run.Total != 10 || run.Processed != 10 || run.Missing != 8 || run.Proposed != 6 || run.Existing != 2 || run.NotFound != 2 {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !reflect.DeepEqual(before, candidate(t, s, 1001)) {
		t.Fatal("preview mutated profile")
	}
	_, err := audit.New(s).Approve(proposal(t, s, run.ID, 1001).ID, domain.Approval{})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("preview approved")
	}
}
func TestApprovalIsolationAuditAndDuplicate(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	before := candidate(t, s, 1001)
	service := audit.New(s)
	id := proposal(t, s, run.ID, 1001).ID
	result, err := service.Approve(id, domain.Approval{})
	must(t, err)
	after := candidate(t, s, 1001)
	if after.Country == nil || *after.Country != "AU" || after.Version != before.Version+1 {
		t.Fatal("country not applied")
	}
	after.Country = before.Country
	after.Version = before.Version
	if !reflect.DeepEqual(before, after) {
		t.Fatal("unselected fields changed")
	}
	if _, err = service.Approve(id, domain.Approval{}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("duplicate approved")
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		writes, e := r.Writes(run.ID)
		if len(writes) != 1 || writes[0].ID != *result.WritebackID {
			t.Fatal("duplicate/missing audit write")
		}
		return e
	}))
}
func TestConcurrentApprovalsAndUndoAcrossConnections(t *testing.T) {
	s, w, path := setup(t)
	other, err := storage.Open(path)
	must(t, err)
	defer other.Close()
	run := completed(t, s, w, "review")
	id := proposal(t, s, run.ID, 1001).ID
	services := []*audit.Review{audit.New(s), audit.New(other)}
	var group sync.WaitGroup
	out := make(chan domain.ReviewResult, 2)
	failures := make(chan error, 2)
	for _, service := range services {
		group.Go(func() {
			r, e := service.Approve(id, domain.Approval{})
			if e != nil {
				failures <- e
			} else {
				out <- r
			}
		})
	}
	group.Wait()
	if len(out) != 1 || len(failures) != 1 || !errors.Is(<-failures, domain.ErrConflict) {
		t.Fatal("approval did not have one effect")
	}
	writeID := *(<-out).WritebackID
	states := make(chan string, 2)
	for _, service := range services {
		group.Go(func() {
			state, e := service.Undo(writeID)
			if e != nil {
				failures <- e
			} else {
				states <- state
			}
		})
	}
	group.Wait()
	if len(states) != 1 || len(failures) != 1 || <-states != "undone" || !errors.Is(<-failures, domain.ErrConflict) {
		t.Fatal("undo did not have one effect")
	}
	if candidate(t, s, 1001).Country != nil {
		t.Fatal("undo failed")
	}
}
func TestFilledAfterScanSkipsApproval(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.EditCountry(1001, domain.String("SG")); return err }))
	result, err := audit.New(s).Approve(proposal(t, s, run.ID, 1001).ID, domain.Approval{})
	must(t, err)
	if result.State != "skipped" || *candidate(t, s, 1001).Country != "SG" {
		t.Fatal("existing value overwritten")
	}
}
func TestConflictsNeedCorrectionNote(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	service := audit.New(s)
	id := proposal(t, s, run.ID, 1006).ID
	if _, err := service.Approve(id, domain.Approval{Value: domain.String("NZ")}); err == nil {
		t.Fatal("correction lacked reason")
	}
	_, err := service.Approve(id, domain.Approval{Value: domain.String("NZ"), Reason: "Confirmed current residence."})
	must(t, err)
}
func TestChangedEvidenceRollsBackApproval(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	id := proposal(t, s, run.ID, 1001).ID
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec("UPDATE demo_candidates SET address_country='New Zealand',version=version+1 WHERE id=1001")
		return err
	}))
	if _, err := audit.New(s).Approve(id, domain.Approval{}); err == nil {
		t.Fatal("stale evidence approved")
	}
	if candidate(t, s, 1001).Country != nil || proposal(t, s, run.ID, 1001).State != "pending" {
		t.Fatal("approval did not roll back")
	}
}

func TestNewConflictingEvidenceBlocksApproval(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	id := proposal(t, s, run.ID, 1001).ID
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec("UPDATE demo_candidates SET notes='Country of residence: New Zealand',version=version+1 WHERE id=1001")
		return err
	}))
	if _, err := audit.New(s).Approve(id, domain.Approval{}); err == nil {
		t.Fatal("new conflict approved while old quote still existed")
	}
	if candidate(t, s, 1001).Country != nil || proposal(t, s, run.ID, 1001).State != "pending" {
		t.Fatal("conflict did not roll back approval")
	}
}

type brokenGateway struct{ connectors.Demo }

func (g brokenGateway) FillCountry(c domain.Candidate, value string) (domain.Mutation, error) {
	m, err := g.Demo.FillCountry(c, value)
	if err != nil {
		return m, err
	}
	_, err = g.Repository.Tx.Exec("UPDATE demo_candidates SET notes='Unexpected change' WHERE id=?", c.ID)
	if err != nil {
		return m, err
	}
	m.Candidate, err = g.Get(c.ID)
	return m, err
}
func TestUnexpectedFieldMutationRollsBackEverything(t *testing.T) {
	s, w, _ := setup(t)
	run := completed(t, s, w, "review")
	service := audit.New(s)
	service.Gateway = func(r *storage.Repository) domain.CandidateGateway {
		return brokenGateway{connectors.Demo{Repository: r}}
	}
	if _, err := service.Approve(proposal(t, s, run.ID, 1001).ID, domain.Approval{}); err == nil {
		t.Fatal("bad adapter accepted")
	}
	c := candidate(t, s, 1001)
	if c.Country != nil || c.Notes != "" {
		t.Fatal("candidate not rolled back")
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		writes, err := r.Writes(run.ID)
		if len(writes) != 0 {
			t.Fatal("write persisted")
		}
		return err
	}))
}
func TestUndoRestoresExactEmptyValue(t *testing.T) {
	for _, original := range []*string{nil, domain.String(""), domain.String(" \t\n"), domain.String("\u2003")} {
		t.Run(fmt.Sprintf("%v", original), func(t *testing.T) {
			s, w, _ := setup(t)
			must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.EditCountry(1003, original); return err }))
			run := completed(t, s, w, "review")
			service := audit.New(s)
			result, err := service.Approve(proposal(t, s, run.ID, 1003).ID, domain.Approval{})
			must(t, err)
			state, err := service.Undo(*result.WritebackID)
			must(t, err)
			if state != "undone" || !reflect.DeepEqual(candidate(t, s, 1003).Country, original) {
				t.Fatal("original empty value was not restored")
			}
		})
	}
}
func TestUndoPreservesLaterEdits(t *testing.T) {
	for _, change := range []string{"country", "notes"} {
		t.Run(change, func(t *testing.T) {
			s, w, _ := setup(t)
			run := completed(t, s, w, "review")
			service := audit.New(s)
			result, err := service.Approve(proposal(t, s, run.ID, 1001).ID, domain.Approval{})
			must(t, err)
			must(t, s.Transaction(func(r *storage.Repository) error {
				if change == "country" {
					_, err := r.EditCountry(1001, domain.String("NZ"))
					return err
				}
				_, err := r.Tx.Exec("UPDATE demo_candidates SET notes='Later edit',version=version+1 WHERE id=1001")
				return err
			}))
			state, err := service.Undo(*result.WritebackID)
			must(t, err)
			if state != "undo_skipped" {
				t.Fatal("later edit overwritten")
			}
		})
	}
}
func TestStaleRevisionAndRejection(t *testing.T) {
	s, w, _ := setup(t)
	before := candidate(t, s, 1001)
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.EditCountry(1001, domain.String("NZ")); return err }))
	must(t, s.Transaction(func(r *storage.Repository) error {
		m, err := (connectors.Demo{Repository: r}).FillCountry(before, "AU")
		if m.Applied {
			t.Fatal("stale version changed")
		}
		return err
	}))
	run := completed(t, s, w, "review")
	id := proposal(t, s, run.ID, 1003).ID
	must(t, audit.New(s).Reject(id))
	if proposal(t, s, run.ID, 1003).State != "rejected" || !domain.Empty(candidate(t, s, 1003).Country) {
		t.Fatal("rejection changed candidate")
	}
}
func TestRestartPauseResumeAndCancel(t *testing.T) {
	s, w, path := setup(t)
	run := newRun(t, s, "preview")
	_, err := w.ProcessOne()
	must(t, err)
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, "pause"); return err }))
	if work, err := w.ProcessOne(); work || err != nil {
		t.Fatal("paused run processed")
	}
	reopened, err := storage.Open(path)
	must(t, err)
	defer reopened.Close()
	must(t, reopened.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, "resume"); return err }))
	drain(t, jobs.New(reopened, 3))
	if getRun(t, reopened, run.ID).Processed != 10 {
		t.Fatal("restart lost progress")
	}
	cancelled := newRun(t, reopened, "review")
	must(t, reopened.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(cancelled.ID, "cancel"); return err }))
	if work, err := w.ProcessOne(); work || err != nil {
		t.Fatal("cancelled run processed")
	}
}

type extractorFunc func(domain.Candidate) (*domain.Extraction, error)

func (f extractorFunc) ExtractCountry(c domain.Candidate) (*domain.Extraction, error) { return f(c) }
func TestLeaseFencingAndExpiryRollback(t *testing.T) {
	s, w, _ := setup(t)
	run := newRun(t, s, "review")
	var old, next *domain.Claim
	must(t, s.Transaction(func(r *storage.Repository) error { var err error; old, err = r.Claim(100, 60); return err }))
	must(t, s.Transaction(func(r *storage.Repository) error { var err error; next, err = r.Claim(161, 60); return err }))
	w.Clock = func() float64 { return 161 }
	if err := w.ProcessClaim(*old); !errors.Is(err, domain.ErrLease) {
		t.Fatal("old owner was not fenced")
	}
	must(t, w.ProcessClaim(*next))
	if getRun(t, s, run.ID).Processed != 3 {
		t.Fatal("new owner failed")
	}
	drain(t, w)
	run2 := newRun(t, s, "review")
	now := 300.0
	w.Clock = func() float64 { return now }
	w.Extractor = extractorFunc(func(c domain.Candidate) (*domain.Extraction, error) {
		now += 70
		return (pipeline.RuleCountryExtractor{}).ExtractCountry(c)
	})
	_, err := w.ProcessOne()
	must(t, err)
	// No page can commit after its lease expires, including any suggestions.
	if getRun(t, s, run2.ID).Processed != 0 {
		t.Fatal("expired page committed progress")
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		items, err := r.Suggestions(run2.ID, "", 100)
		if get := len(items); get != 0 {
			t.Fatal("expired page persisted")
		}
		return err
	}))
	w.Extractor = pipeline.RuleCountryExtractor{}
	drain(t, w)
	if getRun(t, s, run2.ID).State != "completed" {
		t.Fatal("expired job could not recover")
	}
}
func TestRetriesDoNotPersistPrivateErrors(t *testing.T) {
	s, w, _ := setup(t)
	now := 100.0
	w.Clock = func() float64 { return now }
	w.MaxAttempts = 2
	w.Extractor = extractorFunc(func(domain.Candidate) (*domain.Extraction, error) { return nil, errors.New("private candidate data") })
	run := newRun(t, s, "review")
	_, err := w.ProcessOne()
	must(t, err)
	if work, err := w.ProcessOne(); work || err != nil {
		t.Fatal("retry ignored backoff")
	}
	now += 3
	_, err = w.ProcessOne()
	must(t, err)
	failed := getRun(t, s, run.ID)
	if failed.State != "failed" || failed.Processed != 0 || failed.Error == nil || *failed.Error == "private candidate data" {
		t.Fatal("bad retry outcome")
	}
	must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.ChangeRun(run.ID, "resume"); return err }))
	w.Extractor = pipeline.RuleCountryExtractor{}
	drain(t, w)
	if getRun(t, s, run.ID).State != "completed" {
		t.Fatal("resume failed")
	}
}

func TestCheckpointDatabaseFailureRollsBackAndRetries(t *testing.T) {
	s, w, _ := setup(t)
	w.Clock = func() float64 { return 100 }
	run := newRun(t, s, "review")
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON jobs WHEN OLD.state='leased' AND NEW.state='queued' AND NEW.attempts=0 BEGIN SELECT RAISE(FAIL,'private provider detail'); END`)
		return err
	}))
	_, err := w.ProcessOne()
	must(t, err)
	if getRun(t, s, run.ID).Processed != 0 {
		t.Fatal("failed checkpoint committed progress")
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		items, err := r.Suggestions(run.ID, "", 100)
		if err != nil {
			return err
		}
		if len(items) != 0 {
			t.Fatal("failed checkpoint committed proposals")
		}
		var state string
		var available float64
		err = r.Tx.QueryRow("SELECT state,available_at FROM jobs WHERE id=?", run.ID).Scan(&state, &available)
		if state != "queued" || available != 102 {
			t.Fatal("database error was misclassified as lease expiry")
		}
		return err
	}))
}
func TestConcurrentClaimsAcrossConnections(t *testing.T) {
	s, _, path := setup(t)
	newRun(t, s, "review")
	other, err := storage.Open(path)
	must(t, err)
	defer other.Close()
	results := make(chan *domain.Claim, 4)
	failures := make(chan error, 4)
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		store := s
		if i%2 == 1 {
			store = other
		}
		group.Go(func() {
			err := store.Transaction(func(r *storage.Repository) error { c, e := r.Claim(100, 60); results <- c; return e })
			failures <- err
		})
	}
	group.Wait()
	close(results)
	owners := 0
	for c := range results {
		if c != nil {
			owners++
		}
	}
	for i := 0; i < 4; i++ {
		must(t, <-failures)
	}
	if owners != 1 {
		t.Fatal("multiple claim owners")
	}
}
func TestBounded1010ProfileRunExcludesLaterProfiles(t *testing.T) {
	s, w, _ := setup(t)
	must(t, s.Transaction(func(r *storage.Repository) error {
		for i := 2000; i < 3000; i++ {
			if err := r.AddCandidate(domain.Candidate{ID: i, Name: "Synthetic load", AddressCountry: domain.String("AU")}); err != nil {
				return err
			}
		}
		return nil
	}))
	run := newRun(t, s, "review")
	must(t, s.Transaction(func(r *storage.Repository) error {
		return r.AddCandidate(domain.Candidate{ID: 9999, Name: "Added later", AddressCountry: domain.String("NZ")})
	}))
	w.PageSize = 100
	previous, pages := 0, 0
	for {
		processed, err := w.ProcessOne()
		must(t, err)
		if !processed {
			break
		}
		pages++
		current := getRun(t, s, run.ID)
		if current.Processed-previous > 100 || pages > 12 {
			t.Fatal("unbounded page")
		}
		previous = current.Processed
	}
	finished := getRun(t, s, run.ID)
	if finished.Processed != 1010 || finished.Proposed != 1006 || finished.Cursor != 2999 {
		t.Fatalf("wrong bounded result: %+v", finished)
	}
}
func TestExtractionDoesNotHoldDatabaseTransaction(t *testing.T) {
	s, w, _ := setup(t)
	newRun(t, s, "review")
	w.Extractor = extractorFunc(func(c domain.Candidate) (*domain.Extraction, error) {
		must(t, s.Transaction(func(r *storage.Repository) error { _, err := r.Candidate(c.ID); return err }))
		return (pipeline.RuleCountryExtractor{}).ExtractCountry(c)
	})
	_, err := w.ProcessOne()
	must(t, err)
}
