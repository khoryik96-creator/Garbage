package integration

import (
	"context"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/policy"
)

func TestResidenceRules(t *testing.T) {
	tests := []struct {
		name, notes, address, want string
		proposal                   bool
	}{
		{"label", "Country of residence: Malaysia", "", "MY", true}, {"alias", "Based in: UK", "", "GB", true}, {"address", "", "Australia", "AU", true}, {"agreement", "Residence country: NZ", "New Zealand", "NZ", true}, {"conflict", "Country of residence: New Zealand", "Australia", "", true}, {"unknown", "", "Atlantis", "", true}, {"nationality", "Nationality: Malaysian", "", "", false}, {"phone", "Phone: +60123456789", "", "", false}, {"city", "Based in: Sydney", "", "", true}, {"history", "Previously worked in Australia", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := domain.Candidate{Notes: test.notes}
			if test.address != "" {
				c.AddressCountry = &test.address
			}
			result, err := (pipeline.RuleCountryExtractor{}).ExtractCountry(context.Background(), c)
			must(t, err)
			if (result != nil) != test.proposal {
				t.Fatal("incorrect proposal presence")
			}
			if result != nil {
				must(t, policy.ValidateEvidence(c, *result))
				if test.want == "" {
					if result.Value != nil || result.Confidence != "low" {
						t.Fatal("ambiguity lost")
					}
				} else if result.Value == nil || *result.Value != test.want {
					t.Fatal("wrong country")
				}
			}
		})
	}
}
func TestEvidenceValidation(t *testing.T) {
	candidate := domain.Candidate{Notes: "Based in: Malaysia"}
	cases := []domain.Extraction{
		{Value: domain.String("MY"), Confidence: "high"},
		{Value: domain.String("MY"), Confidence: "high", Evidence: []domain.Evidence{{Source: "notes", Quote: "invented"}}},
		{Value: domain.String("MY"), Confidence: "high", Evidence: []domain.Evidence{{Source: "nationality", Quote: "Based in: Malaysia"}}},
		{Value: domain.String("Atlantis"), Confidence: "high", Evidence: []domain.Evidence{{Source: "notes", Quote: candidate.Notes}}},
		{Value: domain.String("MY"), Confidence: "unknown", Evidence: []domain.Evidence{{Source: "notes", Quote: candidate.Notes}}},
	}
	for i, result := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			if policy.ValidateEvidence(candidate, result) == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}
func TestKanoCompositeCountry(t *testing.T) {
	cases := []struct {
		name           string
		address        any
		presence, code string
	}{
		{"both", map[string]any{"country": "Malaysia", "countryCode": "MY"}, "existing", "MY"}, {"name_only", map[string]any{"country": "Malaysia"}, "existing", "MY"}, {"code_only", map[string]any{"countryCode": "MY"}, "existing", "MY"}, {"name_blank_code", map[string]any{"country": "Malaysia", "countryCode": ""}, "existing", "MY"}, {"placeholder", map[string]any{"country": "Unknown", "countryCode": ""}, "existing", ""}, {"conflict", map[string]any{"country": "Malaysia", "countryCode": "SG"}, "existing", ""}, {"both_null", map[string]any{"country": nil, "countryCode": nil}, "empty", ""}, {"whitespace", map[string]any{"country": " \t", "countryCode": ""}, "empty", ""}, {"missing_component", map[string]any{"country": ""}, "unavailable", ""}, {"malformed", map[string]any{"country": "Malaysia", "countryCode": 0}, "unavailable", ""}, {"missing_address", nil, "unavailable", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			observed := connectors.ObserveKanoCountry(map[string]any{"address": test.address})
			if observed.Presence != test.presence {
				t.Fatal("incorrect presence")
			}
			if test.code != "" && (observed.Normalized == nil || *observed.Normalized != test.code) {
				t.Fatal("incorrect normalized country")
			}
		})
	}
}
