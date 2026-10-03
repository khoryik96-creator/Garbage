package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
)

func TestSharedResidenceTextContract(t *testing.T) {
	data, err := os.ReadFile("../../contracts/residence-text-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Text string
		Value      *string
		Quotes     []string
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no shared contract cases")
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			candidate := domain.Candidate{ID: 1003, Notes: test.Text}
			result, err := (RuleCountryExtractor{}).ExtractCountry(context.Background(), candidate)
			if err != nil {
				t.Fatal(err)
			}
			if len(test.Quotes) == 0 {
				if result != nil {
					t.Fatal("unlabelled text produced a proposal")
				}
				return
			}
			if result == nil || !reflect.DeepEqual(result.Value, test.Value) {
				t.Fatalf("incorrect proposal: %+v", result)
			}
			if err = policy.ValidateEvidence(candidate, *result); err != nil {
				t.Fatal(err)
			}
			var quotes []string
			for _, e := range result.Evidence {
				if e.Source != "notes" {
					t.Fatal("incorrect evidence source")
				}
				quotes = append(quotes, e.Quote)
			}
			if !reflect.DeepEqual(quotes, test.Quotes) {
				t.Fatalf("source quote changed: %q", quotes)
			}
			wantConfidence := "low"
			if test.Value != nil {
				wantConfidence = "high"
			}
			if result.Confidence != wantConfidence {
				t.Fatal("incorrect confidence")
			}
		})
	}
}
