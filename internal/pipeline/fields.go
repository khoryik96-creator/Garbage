package pipeline

import (
	"context"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
	"regexp"
	"strings"
)

// Only explicit labeled source lines qualify; do not infer roles, phone types,
// location, or availability from unrelated text.
func ExtractField(ctx context.Context, c domain.Candidate, field string) (*domain.Extraction, error) {
	if field == "country" {
		return (RuleCountryExtractor{}).ExtractCountry(ctx, c)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !domain.RunField(field) {
		return nil, domain.Invalid("Unsupported field.")
	}
	label := strings.ToLower(domain.FieldLabel(field))
	aliases := map[string]string{"linkedin_url": "linkedin", "current_position": "current position|current job title", "notice_period": "notice period|availability"}
	if v, ok := aliases[field]; ok {
		label = v
	}
	re := regexp.MustCompile(`(?i)^(?:` + label + `):\s*(.+?)\s*$`)
	evidence := []domain.Evidence{}
	values := map[string]bool{}
	invalid := false
	for _, line := range lineBreak.Split(c.Notes, -1) {
		match := re.FindStringSubmatch(trimWhitespace(line))
		if match == nil {
			continue
		}
		evidence = append(evidence, domain.Evidence{Source: "notes", Quote: line})
		value, err := policy.FieldValue(field, match[1])
		if err != nil {
			invalid = true
		} else {
			values[value] = true
		}
	}
	if len(evidence) == 0 {
		return nil, nil
	}
	result := &domain.Extraction{Evidence: evidence, Confidence: "low", Reason: "Sources conflict or require a correction. Review the original text."}
	if len(values) == 1 && !invalid {
		for value := range values {
			result.Value = &value
		}
		result.Confidence = "high"
		result.Reason = "An explicit labeled value was found in the source."
	}
	return result, nil
}
