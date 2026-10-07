package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrNotFound = errors.New("record not found")
	ErrConflict = errors.New("record changed or already reviewed; refresh and try again")
	ErrLease    = errors.New("job ownership changed or lease expired")
)

type PolicyError string

func (e PolicyError) Error() string { return string(e) }
func Invalid(message string) error  { return PolicyError(message) }
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Empty(value *string) bool    { return value == nil || strings.TrimSpace(*value) == "" }
func String(value string) *string { return &value }

type Candidate struct {
	ID             int               `json:"id"`
	Name           string            `json:"name"`
	Country        *string           `json:"country"`
	AddressCountry *string           `json:"address_country"`
	Notes          string            `json:"notes"`
	OtherFields    map[string]string `json:"other_fields"`
	Version        int               `json:"version"`
}
type CandidatePage struct {
	Candidates []Candidate `json:"candidates"`
	Cursor     int         `json:"cursor"`
	Finished   bool        `json:"finished"`
}
type Evidence struct {
	Source string `json:"source"`
	Quote  string `json:"quote"`
}
type Extraction struct {
	Value      *string    `json:"value"`
	Confidence string     `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
	Reason     string     `json:"reason"`
}
type RunRequest struct {
	Fields     []string `json:"fields"`
	Mode       string   `json:"mode"`
	Protection string   `json:"protection,omitempty"`
}

func (r RunRequest) Validate() error {
	if r.Mode != "preview" && r.Mode != "review" {
		return Invalid("Choose Preview or Review.")
	}
	if len(r.Fields) == 0 || len(r.Fields) > 20 {
		return Invalid("Select at least one field.")
	}
	seen := map[string]bool{}
	for _, field := range r.Fields {
		if !RunField(field) || seen[field] {
			return Invalid("Select supported fields once each.")
		}
		seen[field] = true
	}
	if r.Protection != "" && r.Protection != "fill_blanks" {
		return Invalid("Choose a valid preservation rule.")
	}
	return nil
}

type Approval struct {
	Value  *string `json:"value"`
	Reason string  `json:"reason"`
}
type Run struct {
	ID         string   `json:"id"`
	Protection string   `json:"protection"`
	Mode       string   `json:"mode"`
	Fields     []string `json:"fields"`
	State      string   `json:"state"`
	Total      int      `json:"total"`
	Processed  int      `json:"processed"`
	Missing    int      `json:"missing"`
	Proposed   int      `json:"proposed"`
	Existing   int      `json:"existing"`
	NotFound   int      `json:"not_found"`
	Cursor     int      `json:"cursor"`
	UpperBound int      `json:"upper_bound"`
	CreatedAt  float64  `json:"created_at"`
	Error      *string  `json:"error"`
}
type Suggestion struct {
	ID            string     `json:"id"`
	RunID         string     `json:"run_id"`
	CandidateID   int        `json:"candidate_id"`
	CandidateName string     `json:"candidate_name"`
	Field         string     `json:"field"`
	Value         *string    `json:"value"`
	Confidence    string     `json:"confidence"`
	Evidence      []Evidence `json:"evidence"`
	Reason        string     `json:"reason"`
	State         string     `json:"state"`
}
type Writeback struct {
	ID           string  `json:"id"`
	SuggestionID string  `json:"suggestion_id"`
	Field        string  `json:"field"`
	RunID        string  `json:"run_id"`
	CandidateID  int     `json:"candidate_id"`
	BeforeValue  *string `json:"before_value"`
	AfterValue   string  `json:"after_value"`
	AfterVersion int     `json:"after_version"`
	GuardVersion int     `json:"guard_version"`
	State        string  `json:"state"`
	CreatedAt    float64 `json:"created_at"`
}
type AuditEvent struct {
	ID          string         `json:"id"`
	RunID       *string        `json:"run_id"`
	CandidateID *int           `json:"candidate_id"`
	Action      string         `json:"action"`
	Details     map[string]any `json:"details"`
	CreatedAt   float64        `json:"created_at"`
}
type Counts struct {
	Total      int `json:"total"`
	Missing    int `json:"missing"`
	Existing   int `json:"existing"`
	UpperBound int `json:"upper_bound"`
}
type Mutation struct {
	Applied   bool
	Candidate Candidate
}
type Claim struct {
	RunID, Token string
	Attempt      int
}
type ReviewResult struct {
	State       string  `json:"state"`
	WritebackID *string `json:"writeback_id"`
}
type CandidateGateway interface {
	Page(after, through, limit int) (CandidatePage, error)
	Get(id int) (Candidate, error)
	FillCountry(Candidate, string) (Mutation, error)
	ClearCountry(Candidate, string, *string) (Mutation, error)
}
type Extractor interface {
	ExtractCountry(context.Context, Candidate) (*Extraction, error)
}

// FieldGateway extends legacy Country adapters without weakening their guards.
type FieldGateway interface {
	FillField(Candidate, string, string) (Mutation, error)
	ClearField(Candidate, string, string, *string) (Mutation, error)
}

func (c Candidate) FieldValue(field string) *string {
	if field == "country" {
		return c.Country
	}
	if field == "name" {
		return &c.Name
	}
	if v, ok := c.OtherFields[field]; ok && (field != "current_position" || !Empty(&v)) {
		return &v
	}
	if field == "current_position" {
		if v, ok := c.OtherFields["title"]; ok {
			return &v
		}
	}
	if v, ok := c.OtherFields[field]; ok {
		return &v
	}
	return nil
}

// StoredFieldValue returns the canonical value without preservation aliases.
// Use this for exact restoration, including an absent key or whitespace.
func (c Candidate) StoredFieldValue(field string) *string {
	if field == "country" {
		return c.Country
	}
	if v, ok := c.OtherFields[field]; ok {
		return &v
	}
	return nil
}
