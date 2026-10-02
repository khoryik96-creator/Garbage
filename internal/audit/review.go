package audit

import (
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/policy"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

type Review struct {
	Store   *storage.Store
	Gateway func(*storage.Repository) domain.CandidateGateway
}

func New(s *storage.Store) *Review { return &Review{Store: s, Gateway: connectors.Factory} }
func (service *Review) Approve(id string, a domain.Approval) (domain.ReviewResult, error) {
	result := domain.ReviewResult{}
	if utf8.RuneCountInString(a.Reason) > 500 {
		return result, domain.Invalid("Correction note is limited to 500 characters.")
	}
	err := service.Store.Transaction(func(r *storage.Repository) error {
		s, err := r.Suggestion(id)
		if err != nil {
			return err
		}
		run, err := r.Run(s.RunID)
		if err != nil {
			return err
		}
		if run.Mode != "review" || run.State != "completed" || len(run.Fields) != 1 || run.Fields[0] != "country" {
			return domain.ErrConflict
		}
		raw := ""
		if a.Value != nil && *a.Value != "" {
			raw = *a.Value
		} else if s.Value != nil {
			raw = *s.Value
		}
		value := policy.Normalize(raw)
		if value == "" {
			return domain.Invalid("Choose a valid country before approving.")
		}
		if (s.Value == nil || value != *s.Value) && strings.TrimSpace(a.Reason) == "" {
			return domain.Invalid("Explain the country correction so it can be audited.")
		}
		if err = r.SuggestionState(id, "pending", "applied"); err != nil {
			return err
		}
		gateway := service.Gateway(r)
		before, err := gateway.Get(s.CandidateID)
		if err != nil {
			return err
		}
		skip := func(reason string) error {
			if err := r.SuggestionState(id, "applied", "skipped"); err != nil {
				return err
			}
			result.State = "skipped"
			return r.Audit("approval_skipped", &run.ID, &before.ID, map[string]any{"reason": reason})
		}
		if !domain.Empty(before.Country) {
			return skip("Country was already filled.")
		}
		if err = policy.ValidateEvidence(before, domain.Extraction{Value: s.Value, Confidence: s.Confidence, Evidence: s.Evidence, Reason: s.Reason}); err != nil {
			return err
		}
		fresh, err := (pipeline.RuleCountryExtractor{}).ExtractCountry(before)
		if err != nil {
			return err
		}
		if fresh == nil || !reflect.DeepEqual(fresh.Value, s.Value) {
			return domain.Invalid("Residence evidence changed. Start a new review run.")
		}
		mutation, err := gateway.FillCountry(before, value)
		if err != nil {
			return err
		}
		if !mutation.Applied {
			return skip("Candidate changed during approval.")
		}
		after := mutation.Candidate
		comparison := after
		comparison.Country = before.Country
		comparison.Version = before.Version
		if !reflect.DeepEqual(comparison, before) || after.Country == nil || *after.Country != value {
			return domain.Invalid("Unexpected field changes. Approval was rolled back.")
		}
		writeID, err := r.RecordWrite(s, before, after)
		if err != nil {
			return err
		}
		result = domain.ReviewResult{State: "applied", WritebackID: &writeID}
		return r.Audit("country_approved", &run.ID, &before.ID, map[string]any{"field": "country", "before": before.Country, "after": value, "writeback_id": writeID, "reason": a.Reason, "actor": "local reviewer"})
	})
	return result, err
}
func (service *Review) Reject(id string) error {
	return service.Store.Transaction(func(r *storage.Repository) error {
		s, err := r.Suggestion(id)
		if err != nil {
			return err
		}
		run, err := r.Run(s.RunID)
		if err != nil {
			return err
		}
		if run.Mode != "review" || run.State != "completed" {
			return domain.ErrConflict
		}
		if err = r.SuggestionState(id, "pending", "rejected"); err != nil {
			return err
		}
		return r.Audit("country_rejected", &run.ID, &s.CandidateID, nil)
	})
}
func (service *Review) Undo(id string) (string, error) {
	state := ""
	err := service.Store.Transaction(func(r *storage.Repository) error {
		write, err := r.Write(id)
		if err != nil {
			return err
		}
		if err = r.ReserveUndo(id); err != nil {
			return err
		}
		if !domain.Empty(write.BeforeValue) {
			return domain.Invalid("Undo must restore the original empty value.")
		}
		gateway := service.Gateway(r)
		current, err := gateway.Get(write.CandidateID)
		if err != nil {
			return err
		}
		state = "undo_skipped"
		if current.Country != nil && *current.Country == write.AfterValue && current.Version == write.AfterVersion {
			m, err := gateway.ClearCountry(current, write.AfterValue, write.BeforeValue)
			if err != nil {
				return err
			}
			if m.Applied {
				comparison := m.Candidate
				comparison.Country = current.Country
				comparison.Version = current.Version
				if !reflect.DeepEqual(comparison, current) || !reflect.DeepEqual(m.Candidate.Country, write.BeforeValue) {
					return domain.Invalid("Unexpected changes during undo. Rolled back.")
				}
				state = "undone"
			}
		}
		if err = r.WriteState(id, state); err != nil {
			return err
		}
		return r.Audit("country_undo", &write.RunID, &write.CandidateID, map[string]any{"writeback_id": id, "state": state})
	})
	return state, err
}
