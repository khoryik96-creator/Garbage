package docworker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

func TestContractAndUntrustedResponses(t *testing.T) {
	for _, test := range []string{"valid", "wrong_candidate", "wrong_source", "wrong_version", "invented_quote", "wrong_country", "unsupported_source", "redirect", "unavailable", "extra_field"} {
		t.Run(test, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request Request
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				if request.ProtocolVersion != 1 || len(request.Fields) != 1 || request.Fields[0] != "country" {
					t.Fatal("incorrect request")
				}
				response := Response{ProtocolVersion: 1, CandidateID: request.CandidateID, SourceID: request.SourceID, Status: "proposed", Extraction: &domain.Extraction{Value: domain.String("MY"), Confidence: "high", Evidence: []domain.Evidence{{Source: "notes", Quote: request.Text}}, Reason: "Explicit residence."}}
				switch test {
				case "wrong_candidate":
					response.CandidateID++
				case "wrong_source":
					response.SourceID = "different"
				case "wrong_version":
					response.ProtocolVersion = 2
				case "invented_quote":
					response.Extraction.Evidence[0].Quote = "invented"
				case "wrong_country":
					response.Extraction.Value = domain.String("SG")
				case "unsupported_source":
					response.Extraction.Evidence[0].Source = "nationality"
				case "redirect":
					http.Redirect(w, r, "https://example.test", 302)
					return
				case "unavailable":
					w.WriteHeader(503)
					return
				case "extra_field":
					_, _ = w.Write([]byte(`{"protocol_version":1,"candidate_id":1003,"source_id":"source","status":"not_found","extraction":null,"write":true}`))
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.ExtractCountry(domain.Candidate{ID: 1003, Version: 1, Notes: "Based in: Malaysia"})
			if test == "valid" {
				if err != nil || result == nil || *result.Value != "MY" {
					t.Fatalf("valid response failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("untrusted response accepted")
			}
		})
	}
}
func TestRejectNonlocalOrigins(t *testing.T) {
	for _, base := range []string{"https://example.test", "http://example.test", "http://user:pass@127.0.0.1", "http://127.0.0.1/path", "http://127.0.0.1?query=1", "http://127.0.0.1#fragment"} {
		if _, err := New(base); err == nil {
			t.Fatal("unsafe worker origin accepted")
		}
	}
}
