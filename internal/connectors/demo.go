package connectors

import (
	"fmt"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

type Demo struct{ Repository *storage.Repository }

func (d Demo) Get(id int) (domain.Candidate, error) { return d.Repository.Candidate(id) }
func (d Demo) Page(after, through, limit int) (domain.CandidatePage, error) {
	return d.Repository.Page(after, through, limit)
}
func (d Demo) FillCountry(c domain.Candidate, value string) (domain.Mutation, error) {
	if policy.Normalize(value) != value || value == "" {
		return domain.Mutation{}, domain.Invalid("Use a valid country code.")
	}
	return d.Repository.MutateCountry(c, &value, nil)
}
func (d Demo) ClearCountry(c domain.Candidate, expected string, restore *string) (domain.Mutation, error) {
	if !domain.Empty(restore) {
		return domain.Mutation{}, domain.Invalid("Undo must restore an empty value.")
	}
	return d.Repository.MutateCountry(c, restore, &expected)
}
func Factory(r *storage.Repository) domain.CandidateGateway { return Demo{Repository: r} }
func Seed(r *storage.Repository) error {
	counts, err := r.Counts()
	if err != nil || counts.Total != 0 {
		return err
	}
	profiles := []domain.Candidate{
		{ID: 1001, Name: "Alex Morgan", AddressCountry: domain.String("Australia")},
		{ID: 1002, Name: "Jordan Lee", Country: domain.String("NZ"), AddressCountry: domain.String("New Zealand")},
		{ID: 1003, Name: "Sam Taylor", Country: domain.String(""), Notes: "Country of residence: New Zealand"},
		{ID: 1004, Name: "Casey Reed", Notes: "Based in: United Kingdom"},
		{ID: 1005, Name: "Avery Quinn", Notes: "Previously worked in Australia. Citizenship: Canadian."},
		{ID: 1006, Name: "Riley Park", AddressCountry: domain.String("Australia"), Notes: "Country of residence: New Zealand"},
		{ID: 1007, Name: "Drew Ellis", Country: domain.String("Unknown"), AddressCountry: domain.String("Singapore")},
		{ID: 1008, Name: "Jamie Blake", AddressCountry: domain.String("Singapore")},
		{ID: 1009, Name: "Cameron Gray", Notes: "No residence information supplied."},
		{ID: 1010, Name: "Robin Lane", AddressCountry: domain.String("Atlantis")},
	}
	for _, c := range profiles {
		c.Name = "Demo " + c.Name
		c.OtherFields = map[string]string{"email": fmt.Sprintf("demo%d@example.invalid", c.ID), "title": "Demo analyst"}
		if err := r.AddCandidate(c); err != nil {
			return err
		}
	}
	return r.Audit("demo_seeded", nil, nil, map[string]any{"profiles": len(profiles)})
}
