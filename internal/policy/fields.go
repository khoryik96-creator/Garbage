package policy

import (
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

func FieldValue(field, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if field == "country" {
		if code := Normalize(value); code != "" {
			return code, nil
		}
		return "", domain.Invalid("Choose a valid country.")
	}
	if !domain.RunField(field) || value == "" || utf8.RuneCountInString(value) > 500 || strings.ContainsAny(value, "\n\r\x00") {
		return "", domain.Invalid("Enter a supported, nonempty field value.")
	}
	switch field {
	case "email":
		parsed, err := mail.ParseAddress(value)
		if err != nil || parsed.Address != value || !strings.Contains(value, "@") {
			return "", domain.Invalid("Enter one valid email address.")
		}
	case "mobile", "phone":
		if !regexp.MustCompile(`^[+0-9(). -]{5,40}$`).MatchString(value) || len(regexp.MustCompile(`[^0-9]`).ReplaceAllString(value, "")) < 5 {
			return "", domain.Invalid("Enter a complete phone number.")
		}
	case "linkedin_url":
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !(u.Hostname() == "linkedin.com" || strings.HasSuffix(u.Hostname(), ".linkedin.com")) || u.Path == "" || u.Path == "/" {
			return "", domain.Invalid("Enter an HTTPS LinkedIn profile URL.")
		}
	}
	return value, nil
}
func ValidateFieldEvidence(c domain.Candidate, field string, e domain.Extraction) error {
	if field == "country" {
		return ValidateEvidence(c, e)
	}
	if !domain.RunField(field) || len(e.Evidence) == 0 || (e.Confidence != "high" && e.Confidence != "low") {
		return domain.Invalid("Every proposal needs valid source evidence.")
	}
	for _, quote := range e.Evidence {
		if quote.Source != "notes" || strings.TrimSpace(quote.Quote) == "" || !strings.Contains(c.Notes, quote.Quote) {
			return domain.Invalid("The evidence does not occur in its source.")
		}
	}
	if e.Value != nil {
		v, err := FieldValue(field, *e.Value)
		if err != nil || v != *e.Value {
			return domain.Invalid("The proposed field value is invalid.")
		}
	}
	return nil
}
