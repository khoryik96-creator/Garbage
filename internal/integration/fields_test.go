package integration

import (
	"context"
	"github.com/khoryik96-creator/Garbage/internal/audit"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/storage"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMultiFieldFillPreservesExistingInformationAndSupportsUndo(t *testing.T) {
	s, w, _ := setup(t)
	var before domain.Candidate
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec("UPDATE demo_candidates SET notes=?,version=version+1 WHERE id=1001", "Email: replacement@example.invalid\nMobile: +61 412 345 678\nCurrent employer: Example Analytics")
		return err
	}))
	before = candidate(t, s, 1001)
	var run domain.Run
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		run, err = r.NewRun(domain.RunRequest{Mode: "review", Fields: []string{"country", "email", "mobile", "current_employer"}})
		return err
	}))
	drain(t, w)
	var suggestions []domain.Suggestion
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		suggestions, err = r.Suggestions(run.ID, "", 100)
		return err
	}))

	service := audit.New(s)
	var mobile, employer domain.ReviewResult
	for _, proposal := range suggestions {
		if proposal.CandidateID != 1001 {
			continue
		}
		if proposal.Field == "email" {
			t.Fatal("populated email was proposed")
		}
		result, err := service.Approve(proposal.ID, domain.Approval{})
		must(t, err)
		if proposal.Field == "current_employer" {
			employer = result
		}
		if proposal.Field == "mobile" {
			mobile = result
		}
	}
	after := candidate(t, s, 1001)
	if after.OtherFields["email"] != before.OtherFields["email"] || after.Name != before.Name || after.OtherFields["title"] != before.OtherFields["title"] || after.OtherFields["mobile"] != "+61 412 345 678" || after.OtherFields["current_employer"] != "Example Analytics" {
		t.Fatal("selected blanks or protected data incorrect", after)
	}
	if mobile.WritebackID == nil {
		t.Fatal("mobile did not apply")
	}
	state, err := service.Undo(*mobile.WritebackID)
	must(t, err)
	if state != "undone" || candidate(t, s, 1001).OtherFields["mobile"] != "" {
		t.Fatal("field undo failed")
	}
	backup := filepath.Join(t.TempDir(), "multi-field.db")
	must(t, s.Backup(context.Background(), backup))
	_, err = s.Restore(context.Background(), backup)
	must(t, err)
	if candidate(t, s, 1001).OtherFields["current_employer"] != "Example Analytics" {
		t.Fatal("backup lost multi-field changes")
	}
	state, err = service.Undo(*employer.WritebackID)
	must(t, err)
	if state != "undone" || candidate(t, s, 1001).OtherFields["current_employer"] != "" {
		t.Fatal("second approved field cannot undo")
	}
}
func TestPopulatedFieldAtApprovalIsNeverOverwritten(t *testing.T) {
	s, w, _ := setup(t)
	var run domain.Run
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		run, err = r.NewRun(domain.RunRequest{Mode: "review", Fields: []string{"mobile"}})
		return err
	}))
	drain(t, w)
	var proposals []domain.Suggestion
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		proposals, err = r.Suggestions(run.ID, "", 100)
		return err
	}))
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec(`UPDATE demo_candidates SET other_fields=json_set(other_fields,'$.mobile','Unknown'),version=version+1 WHERE id=1001`)
		return err
	}))
	for _, p := range proposals {
		if p.CandidateID == 1001 {
			result, err := audit.New(s).Approve(p.ID, domain.Approval{})
			must(t, err)
			if result.State != "skipped" {
				t.Fatal("late existing information overwritten")
			}
		}
	}
	if candidate(t, s, 1001).OtherFields["mobile"] != "Unknown" {
		t.Fatal("placeholder value lost")
	}
}

func TestPositionAliasesPreserveExistingValuesAndFillEmptyTitle(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		proposals    int
	}{
		{"empty legacy title", `{"title":""}`, 1},
		{"whitespace canonical title", `{"title":"","current_position":"   "}`, 1},
		{"populated legacy title", `{"title":"Existing role","current_position":""}`, 0},
		{"populated canonical title", `{"title":"","current_position":"Existing role"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, _ := setup(t)
			must(t, s.Transaction(func(r *storage.Repository) error {
				_, err := r.Tx.Exec("UPDATE demo_candidates SET notes='Current position: Source role',other_fields=?,version=version+1 WHERE id=1001", tc.fields)
				return err
			}))
			original := candidate(t, s, 1001)
			var run domain.Run
			must(t, s.Transaction(func(r *storage.Repository) error {
				var err error
				run, err = r.NewRun(domain.RunRequest{Mode: "review", Fields: []string{"current_position"}})
				return err
			}))
			drain(t, w)
			var proposals []domain.Suggestion
			must(t, s.Transaction(func(r *storage.Repository) error {
				var err error
				proposals, err = r.Suggestions(run.ID, "", 100)
				return err
			}))
			count := 0
			for _, p := range proposals {
				if p.CandidateID != 1001 {
					continue
				}
				count++
				result, err := audit.New(s).Approve(p.ID, domain.Approval{})
				must(t, err)
				if result.State != "applied" {
					t.Fatal("blank title could not be filled")
				}
				state, err := audit.New(s).Undo(*result.WritebackID)
				must(t, err)
				if state != "undone" {
					t.Fatal("alias undo failed")
				}
				if !reflect.DeepEqual(original.OtherFields, candidate(t, s, 1001).OtherFields) {
					t.Fatal("undo did not restore exact canonical value")
				}
			}
			if count != tc.proposals {
				t.Fatal("existing alias was ignored", count)
			}
		})
	}
}

func TestMultiFieldUndoKeepsExternalVersionGapBlocked(t *testing.T) {
	s, w, _ := setup(t)
	var run domain.Run
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		run, err = r.NewRun(domain.RunRequest{Mode: "review", Fields: []string{"mobile", "current_employer"}})
		return err
	}))
	drain(t, w)
	var proposals []domain.Suggestion
	must(t, s.Transaction(func(r *storage.Repository) error {
		var err error
		proposals, err = r.Suggestions(run.ID, "", 100)
		return err
	}))
	var first, second domain.ReviewResult
	for _, p := range proposals {
		if p.CandidateID != 1001 || p.Field != "mobile" {
			continue
		}
		var err error
		first, err = audit.New(s).Approve(p.ID, domain.Approval{})
		must(t, err)
	}
	must(t, s.Transaction(func(r *storage.Repository) error {
		_, err := r.Tx.Exec("UPDATE demo_candidates SET other_fields=json_set(other_fields,'$.email','external@example.invalid'),version=version+1 WHERE id=1001")
		return err
	}))
	for _, p := range proposals {
		if p.CandidateID != 1001 || p.Field != "current_employer" {
			continue
		}
		var err error
		second, err = audit.New(s).Approve(p.ID, domain.Approval{})
		must(t, err)
	}
	if first.WritebackID == nil || second.WritebackID == nil {
		t.Fatal("missing test approvals")
	}
	state, err := audit.New(s).Undo(*second.WritebackID)
	must(t, err)
	if state != "undone" {
		t.Fatal("latest change cannot undo")
	}
	state, err = audit.New(s).Undo(*first.WritebackID)
	must(t, err)
	if state != "undo_skipped" {
		t.Fatal("external edit gap was rebased")
	}
	if candidate(t, s, 1001).OtherFields["email"] != "external@example.invalid" {
		t.Fatal("external value changed")
	}
}
