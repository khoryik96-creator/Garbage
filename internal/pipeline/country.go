package pipeline

import (
	"regexp"
	"strings"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
)

var residence = regexp.MustCompile(`(?i)^(?:country of residence|residence country|based in)\s*:\s*(.+?)\s*$`)

type RuleCountryExtractor struct{}

func (RuleCountryExtractor) ExtractCountry(c domain.Candidate) (*domain.Extraction, error) {
	evidence := []domain.Evidence{}
	values := map[string]bool{}
	invalid := false
	add := func(source, quote, value string) {
		evidence = append(evidence, domain.Evidence{Source: source, Quote: quote})
		code := policy.Normalize(value)
		if code == "" {
			invalid = true
		} else {
			values[code] = true
		}
	}
	if !domain.Empty(c.AddressCountry) {
		add("address", *c.AddressCountry, *c.AddressCountry)
	}
	for _, line := range strings.Split(c.Notes, "\n") {
		match := residence.FindStringSubmatch(strings.TrimSpace(line))
		if match != nil {
			add("notes", line, match[1])
		}
	}
	if len(evidence) == 0 {
		return nil, nil
	}
	result := &domain.Extraction{Evidence: evidence, Confidence: "low", Reason: "Sources conflict or contain an unrecognized country. Review required."}
	if !invalid && len(values) == 1 {
		for code := range values {
			result.Value = domain.String(code)
		}
		result.Confidence = "high"
		result.Reason = "An explicit country of residence was found in the source."
	}
	return result, nil
}
