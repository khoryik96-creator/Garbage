package pipeline

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
)

// Match Python's Unicode whitespace and splitlines semantics at the document
// boundary. Keep the original line for evidence rather than normalizing its text.
const whitespace = `[\p{Z}\t-\r\x{0085}\x{001c}-\x{001f}]`

var residence = regexp.MustCompile(`(?i)^(?:country of res[iıİ]dence|res[iıİ]dence country|based [iıİ]n)` + whitespace + `*:` + whitespace + `*(.+?)` + whitespace + `*$`)
var lineBreak = regexp.MustCompile(`\r\n|[\n\v\f\r\x{001c}-\x{001e}\x{0085}\x{2028}\x{2029}]`)

func trimWhitespace(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || (r >= '\x1c' && r <= '\x1f') })
}

type RuleCountryExtractor struct{}

func (RuleCountryExtractor) ExtractCountry(ctx context.Context, c domain.Candidate) (*domain.Extraction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	for _, line := range lineBreak.Split(c.Notes, -1) {
		match := residence.FindStringSubmatch(trimWhitespace(line))
		if match != nil {
			add("notes", line, trimWhitespace(match[1]))
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
