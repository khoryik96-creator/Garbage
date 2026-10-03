package connectors

import (
	"strings"

	"github.com/khoryik96-creator/Garbage/internal/policy"
)

type CountryObservation struct {
	Presence   string  `json:"presence"`
	Normalized *string `json:"normalized"`
	Warning    string  `json:"warning,omitempty"`
}

func ObserveKanoCountry(snapshot map[string]any) CountryObservation {
	address, ok := snapshot["address"].(map[string]any)
	if !ok {
		return CountryObservation{Presence: "unavailable", Warning: "Address fields unavailable."}
	}
	values := []string{}
	present := 0
	for _, key := range []string{"country", "countryCode"} {
		v, exists := address[key]
		if !exists {
			continue
		}
		present++
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return CountryObservation{Presence: "unavailable", Warning: "Unexpected Country shape."}
		}
		if strings.TrimSpace(s) != "" {
			values = append(values, s)
		}
	}
	if len(values) > 0 {
		code := policy.Normalize(values[0])
		for _, v := range values {
			if code == "" || policy.Normalize(v) != code {
				return CountryObservation{Presence: "existing", Warning: "Existing values conflict or are unrecognized."}
			}
		}
		return CountryObservation{Presence: "existing", Normalized: &code}
	}
	if present == 2 {
		return CountryObservation{Presence: "empty"}
	}
	return CountryObservation{Presence: "unavailable", Warning: "A Country component was omitted."}
}
