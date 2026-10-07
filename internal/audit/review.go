package audit

import (
	"context"
	"reflect"
	"slices"
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
		if run.Mode != "review" || run.State != "completed" || !slices.Contains(run.Fields, s.Field) {
			return domain.ErrConflict
		}
		raw := ""
		if a.Value != nil && *a.Value != "" {
			raw = *a.Value
		} else if s.Value != nil {
			raw = *s.Value
		}
		value, err := policy.FieldValue(s.Field, raw)
		if err != nil {
			return err
		}
		if (s.Value == nil || value != *s.Value) && strings.TrimSpace(a.Reason) == "" {
			return domain.Invalid("Explain the correction so it can be audited.")
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
		if !domain.Empty(before.FieldValue(s.Field)) {
			return skip("The field was already filled.")
		}

		if err = policy.ValidateFieldEvidence(before, s.Field, domain.Extraction{Value: s.Value, Confidence: s.Confidence, Evidence: s.Evidence, Reason: s.Reason}); err != nil {
			return err
		}
		fresh, err := pipeline.ExtractField(context.Background(), before, s.Field)
		if err != nil {
			return err
		}
		if fresh == nil || !reflect.DeepEqual(fresh.Value, s.Value) {
			return domain.Invalid("Source evidence changed. Start a new review run.")
		}
		var mutation domain.Mutation
		if s.Field == "country" {
			mutation, err = gateway.FillCountry(before, value)
		} else {
			fields, ok := gateway.(domain.FieldGateway)
			if !ok {
				return domain.Invalid("This adapter cannot update the selected field.")
			}
			mutation, err = fields.FillField(before, s.Field, value)
		}
		if err != nil {
			return err
		}
		if !mutation.Applied {
			return skip("Candidate changed during approval.")
		}
		after := mutation.Candidate
		comparison := after
		resetField(&comparison, before, s.Field)
		comparison.Version = before.Version
		if !reflect.DeepEqual(comparison, before) || after.FieldValue(s.Field) == nil || *after.FieldValue(s.Field) != value {
			return domain.Invalid("Unexpected field changes. Approval was rolled back.")
		}
		writeID, err := r.RecordWrite(s, before, after)
		if err != nil {
			return err
		}
		result = domain.ReviewResult{State: "applied", WritebackID: &writeID}
		action := "field_approved"
		if s.Field == "country" {
			action = "country_approved"
		}
		return r.Audit(action, &run.ID, &before.ID, map[string]any{"field": s.Field, "before": before.FieldValue(s.Field), "after": value, "writeback_id": writeID, "reason": a.Reason, "actor": "local reviewer"})
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
		action := "field_rejected"
		if s.Field == "country" {
			action = "country_rejected"
		}
		return r.Audit(action, &run.ID, &s.CandidateID, map[string]any{"field": s.Field})
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
		if current.FieldValue(write.Field) != nil && *current.FieldValue(write.Field) == write.AfterValue && current.Version == write.GuardVersion {
			var m domain.Mutation
			if write.Field == "country" {
				m, err = gateway.ClearCountry(current, write.AfterValue, write.BeforeValue)
			} else {
				fields, ok := gateway.(domain.FieldGateway)
				if !ok {
					return domain.Invalid("This adapter cannot undo the selected field.")
				}
				m, err = fields.ClearField(current, write.Field, write.AfterValue, write.BeforeValue)
			}
			if err != nil {
				return err
			}
			if m.Applied {
				comparison := m.Candidate
				resetField(&comparison, current, write.Field)
				comparison.Version = current.Version
				if !reflect.DeepEqual(comparison, current) || !reflect.DeepEqual(m.Candidate.StoredFieldValue(write.Field), write.BeforeValue) {
					return domain.Invalid("Unexpected changes during undo. Rolled back.")
				}
				if err = r.AdvanceUndoGuards(current.ID, current.Version, m.Candidate.Version); err != nil {
					return err
				}
				state = "undone"
			}
		}
		if err = r.WriteState(id, state); err != nil {
			return err
		}
		action := "field_undo"
		if write.Field == "country" {
			action = "country_undo"
		}
		return r.Audit(action, &write.RunID, &write.CandidateID, map[string]any{"field": write.Field, "writeback_id": id, "state": state})
	})
	return state, err
}

func resetField(candidate *domain.Candidate, original domain.Candidate, field string) {
	if field == "country" {
		candidate.Country = original.Country
		return
	}
	fields := map[string]string{}
	for k, v := range candidate.OtherFields {
		fields[k] = v
	}
	if v, ok := original.OtherFields[field]; ok {
		fields[field] = v
	} else {
		delete(fields, field)
	}
	candidate.OtherFields = fields
}
