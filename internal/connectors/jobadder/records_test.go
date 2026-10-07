package jobadder

import (
	"encoding/json"
	"testing"
)

func TestSummaryPreservesReturnedContactsAndCountryCode(t *testing.T) {
	profile, err := Summary(json.RawMessage(`{"candidateId":42,"firstName":"Real","lastName":"Person","email":"person@example.invalid","phone":" 03 1234 ","mobile":"+61 412 000 111","address":{"country":null,"countryCode":"AU","state":"Victoria"}}`))
	if err != nil || profile.Name != "Real Person" || profile.Country == nil || *profile.Country != "AU" || profile.AddressCountry == nil || *profile.AddressCountry != "AU" || profile.OtherFields["mobile"] != "+61 412 000 111" || profile.OtherFields["phone"] != " 03 1234 " || profile.OtherFields["state"] != "Victoria" {
		t.Fatal("summary dropped or rewrote returned data", profile, err)
	}
	profile, err = Summary(json.RawMessage(`{"candidateId":42,"email":"","mobile":null,"address":{"country":"Japan","countryCode":"JP"}}`))
	if err != nil || *profile.Country != "Japan" {
		t.Fatal("country name not preserved", err)
	}
	if value, returned := profile.OtherFields["email"]; !returned || value != "" {
		t.Fatal("explicitly empty email lost")
	}
	for _, omitted := range []string{"mobile", "phone", "current_employer", "notes"} {
		if _, present := profile.OtherFields[omitted]; present {
			t.Fatal("unreturned field treated as empty", omitted)
		}
	}
	for _, invalid := range []string{`{`, `{}`, `{"candidateId":0}`, `{"candidateId":42,"email":{"value":"x"}}`} {
		if _, err := Summary(json.RawMessage(invalid)); err == nil {
			t.Fatal("unsupported summary accepted", invalid)
		}
	}
}
