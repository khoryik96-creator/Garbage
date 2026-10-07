package jobadder

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

// Summary is a read-only projection of the public candidate list. It does not
// establish a write mapping or treat omitted detail fields as missing values.
func Summary(raw json.RawMessage) (domain.Candidate, error) {
	var row struct {
		ID        int     `json:"candidateId"`
		FirstName string  `json:"firstName"`
		LastName  string  `json:"lastName"`
		Email     *string `json:"email"`
		Phone     *string `json:"phone"`
		Mobile    *string `json:"mobile"`
		Address   *struct {
			Country     *string `json:"country"`
			CountryCode *string `json:"countryCode"`
			State       *string `json:"state"`
		} `json:"address"`
	}
	if json.Unmarshal(raw, &row) != nil || row.ID <= 0 {
		return domain.Candidate{}, domain.Invalid("JobAdder returned an unsupported candidate summary.")
	}
	name := strings.TrimSpace(row.FirstName + " " + row.LastName)
	if name == "" {
		name = fmt.Sprintf("JobAdder profile #%d", row.ID)
	}
	profile := domain.Candidate{ID: row.ID, Name: name, OtherFields: map[string]string{}}
	for field, value := range map[string]*string{"email": row.Email, "phone": row.Phone, "mobile": row.Mobile} {
		if value != nil {
			profile.OtherFields[field] = *value
		}
	}
	if row.Address != nil {
		profile.Country = row.Address.Country
		profile.AddressCountry = row.Address.CountryCode
		if domain.Empty(profile.Country) && !domain.Empty(row.Address.CountryCode) {
			profile.Country = row.Address.CountryCode
		}
		if row.Address.State != nil {
			profile.OtherFields["state"] = *row.Address.State
		}
	}
	return profile, nil
}
