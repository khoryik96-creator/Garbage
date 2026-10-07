package storage

import (
	"github.com/khoryik96-creator/Garbage/internal/domain"
)

func (r *Repository) MutateField(before domain.Candidate, field string, value, expected *string) (domain.Mutation, error) {
	if field == "country" {
		return r.MutateCountry(before, value, expected)
	}
	if !domain.RunField(field) {
		return domain.Mutation{}, domain.Invalid("Unsupported field.")
	}
	original := before.FieldValue(field)
	if expected == nil && !domain.Empty(original) {
		return domain.Mutation{Candidate: before}, nil
	}
	if expected != nil && (original == nil || *original != *expected) {
		return domain.Mutation{Candidate: before}, nil
	}
	fields := map[string]string{}
	for k, v := range before.OtherFields {
		fields[k] = v
	}
	if value == nil {
		delete(fields, field)
	} else {
		fields[field] = *value
	}
	var stored *string
	if v, ok := before.OtherFields[field]; ok {
		stored = &v
	}
	result, err := r.Tx.Exec("UPDATE demo_candidates SET other_fields=?,version=version+1 WHERE id=? AND version=? AND json_extract(other_fields,?) IS ?", encode(fields), before.ID, before.Version, "$."+field, stored)
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
