package docworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/pipeline"
	"github.com/khoryik96-creator/Garbage/internal/policy"
)

// This local, versioned boundary returns proposals only. It has no database or write credentials.
type Request struct {
	ProtocolVersion int      `json:"protocol_version"`
	CandidateID     int      `json:"candidate_id"`
	SourceID        string   `json:"source_id"`
	Text            string   `json:"text"`
	Fields          []string `json:"fields"`
}
type Response struct {
	ProtocolVersion int                `json:"protocol_version"`
	CandidateID     int                `json:"candidate_id"`
	SourceID        string             `json:"source_id"`
	Status          string             `json:"status"`
	Extraction      *domain.Extraction `json:"extraction"`
}
type Client struct {
	URL  string
	HTTP *http.Client
}

func New(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return nil, domain.Invalid("Document worker must use a local HTTP origin.")
	}
	return &Client{URL: strings.TrimSuffix(base, "/"), HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) ExtractCountry(ctx context.Context, candidate domain.Candidate) (*domain.Extraction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Explicit address evidence stays in the Go policy path. Notes can exercise the document boundary.
	if !domain.Empty(candidate.AddressCountry) {
		return (pipeline.RuleCountryExtractor{}).ExtractCountry(ctx, candidate)
	}
	request := Request{ProtocolVersion: 1, CandidateID: candidate.ID, SourceID: fmt.Sprintf("candidate:%d:notes:v%d", candidate.ID, candidate.Version), Text: candidate.Notes, Fields: []string{"country"}}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/extract", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("document worker unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("document worker request failed")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var result Response
	if err = decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid document worker response")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("invalid document worker response")
	}
	if result.ProtocolVersion != 1 || result.CandidateID != candidate.ID || result.SourceID != request.SourceID {
		return nil, domain.Invalid("Document response identity mismatch.")
	}
	if result.Status == "not_found" && result.Extraction == nil {
		return nil, nil
	}
	if result.Status != "proposed" || result.Extraction == nil {
		return nil, domain.Invalid("Invalid document result.")
	}
	if err = policy.ValidateEvidence(candidate, *result.Extraction); err != nil {
		return nil, err
	}
	// The prototype accepts only deterministic, labelled residence evidence. Adding a model
	// requires a separate policy and evaluation change, never a weaker evidence check.
	expected, err := (pipeline.RuleCountryExtractor{}).ExtractCountry(ctx, candidate)
	if err != nil {
		return nil, err
	}
	if expected == nil {
		return nil, domain.Invalid("No permitted residence evidence.")
	}
	if (expected.Value == nil) != (result.Extraction.Value == nil) || (expected.Value != nil && *expected.Value != *result.Extraction.Value) {
		return nil, domain.Invalid("Document result conflicts with residence evidence.")
	}
	return result.Extraction, nil
}
