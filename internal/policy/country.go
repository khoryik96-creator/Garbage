package policy

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

// Country names/codes exported from pycountry 24.6.1's ISO catalogue.
//
//go:embed countries.json
var countryBytes []byte

type Country struct {
	Code     string `json:"alpha_2"`
	Alpha3   string `json:"alpha_3"`
	Name     string `json:"name"`
	Official string `json:"official_name"`
	Common   string `json:"common_name"`
}

var Countries []Country
var names map[string]string

func init() {
	if err := json.Unmarshal(countryBytes, &Countries); err != nil {
		panic(err)
	}
	names = map[string]string{}
	for _, c := range Countries {
		for _, v := range []string{c.Code, c.Alpha3, c.Name, c.Official, c.Common} {
			if v != "" {
				names[strings.ToLower(v)] = c.Code
			}
		}
	}
	names["uk"] = "GB"
	names["usa"] = "US"
	names["south korea"] = "KR"
}
func Normalize(value string) string { return names[strings.ToLower(strings.TrimSpace(value))] }
func Name(value *string) string {
	if domain.Empty(value) {
		return "Empty"
	}
	code := Normalize(*value)
	for _, c := range Countries {
		if c.Code == code {
			return c.Name
		}
	}
	return *value
}
func ValidateEvidence(candidate domain.Candidate, result domain.Extraction) error {
	if len(result.Evidence) == 0 {
		return domain.Invalid("Every proposal needs source evidence.")
	}
	if result.Confidence != "high" && result.Confidence != "low" {
		return domain.Invalid("Invalid confidence.")
	}
	for _, e := range result.Evidence {
		source := candidate.Notes
		switch e.Source {
		case "address":
			source = ""
			if candidate.AddressCountry != nil {
				source = *candidate.AddressCountry
			}
		case "notes":
		default:
			return domain.Invalid("Unrecognized evidence source.")
		}
		if strings.TrimSpace(e.Quote) == "" || !strings.Contains(source, e.Quote) {
			return domain.Invalid("The evidence quote does not occur in its source.")
		}
	}
	if result.Value != nil && (Normalize(*result.Value) == "" || Normalize(*result.Value) != *result.Value) {
		return domain.Invalid("Use a valid ISO country code.")
	}
	return nil
}
