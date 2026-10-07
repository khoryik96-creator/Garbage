// Package jobadder provides public-v2 read access. Browser SPA payloads and
// remote writes are deliberately absent until their contracts are verified.
package jobadder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

const DefaultBase = "https://api.jobadder.com/v2"

func apiBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v2" || !strings.HasSuffix(u.Hostname(), "api.jobadder.com") {
		return nil, domain.Invalid("Use an HTTPS JobAdder public-v2 API base returned by OAuth.")
	}
	prefix := strings.TrimSuffix(u.Hostname(), "api.jobadder.com")
	if strings.ContainsAny(prefix, "/.@:") {
		return nil, domain.Invalid("Invalid JobAdder regional API host.")
	}
	return u, nil
}

type Page struct {
	Items      []json.RawMessage `json:"items"`
	TotalCount int               `json:"totalCount"`
	Links      struct {
		Next string `json:"next"`
	} `json:"links"`
}

// Raw records retain account-specific fields for later schema verification.
// They are never treated as the demo Candidate model or sent to a write route.
type Client struct {
	base  *url.URL
	Token func(context.Context) (string, error)
	HTTP  *http.Client
}

func New(base string, token func(context.Context) (string, error)) (*Client, error) {
	u, err := apiBase(base)
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, domain.Invalid("Configure JobAdder authorization first.")
	}
	return &Client{base: u, Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}}, nil
}

func (c *Client) pageURL(next string) (*url.URL, error) {
	u := *c.base
	u.Path += "/candidates"
	if next != "" {
		parsed, err := url.Parse(next)
		if err != nil {
			return nil, domain.Invalid("Invalid JobAdder pagination link.")
		}
		u = *u.ResolveReference(parsed)
	}
	if u.Scheme != c.base.Scheme || u.Host != c.base.Host || u.User != nil || u.Fragment != "" || u.Path != c.base.Path+"/candidates" {
		return nil, domain.Invalid("JobAdder pagination must stay on this account's candidate API.")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, domain.Invalid("Invalid JobAdder pagination query.")
	}
	u.RawQuery = query.Encode()
	u.RawPath = ""
	return &u, nil
}

type HTTPError struct {
	Status     int
	RetryAfter time.Duration
}

func (e HTTPError) Error() string {
	switch e.Status {
	case 401:
		return "JobAdder authorization expired. Reconnect the account."
	case 403:
		return "JobAdder denied candidate read access. Check the app's read scopes."
	case 429:
		return "JobAdder rate limit reached. Retry after the account budget is available."
	default:
		return fmt.Sprintf("JobAdder request failed (HTTP %d).", e.Status)
	}
}

func retryAfter(raw string) time.Duration {
	if seconds, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		return time.Duration(min(seconds, uint64(math.MaxInt64/int64(time.Second)))) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		return max(time.Until(at), 0)
	}
	return time.Second
}

func (c *Client) Candidates(ctx context.Context, next string) (Page, error) {
	// Bound the entire lookup, including rate-limit waits, without shortening a
	// provider's Retry-After. Return the full delay when it exceeds our budget.
	budget := c.HTTP.Timeout
	if budget <= 0 {
		budget = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var page Page
	u, err := c.pageURL(next)
	if err != nil {
		return page, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		token, err := c.Token(ctx)
		if err != nil {
			return page, domain.Invalid("JobAdder authorization is unavailable. Reconnect the account.")
		}
		if token == "" {
			return page, domain.Invalid("Configure JobAdder authorization first.")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return page, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		client := *c.HTTP
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(req)
		if err != nil {
			return page, domain.Invalid("JobAdder could not be reached. Check the connection and retry.")
		}
		if response.StatusCode != 200 {
			_ = response.Body.Close()
			failure := HTTPError{Status: response.StatusCode, RetryAfter: retryAfter(response.Header.Get("Retry-After"))}
			if response.StatusCode != 429 || attempt == 2 {
				return page, failure
			}
			if deadline, ok := ctx.Deadline(); ok && failure.RetryAfter >= time.Until(deadline) {
				return page, failure
			}
			select {
			case <-ctx.Done():
				return page, ctx.Err()
			case <-time.After(failure.RetryAfter):
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
		response.Body.Close()
		if err != nil || len(body) > 8<<20 {
			return page, domain.Invalid("JobAdder returned an invalid or oversized candidate page.")
		}
		if err = json.Unmarshal(body, &page); err != nil || page.Items == nil || page.TotalCount < 0 {
			return page, domain.Invalid("JobAdder returned an unsupported candidate page schema.")
		}
		for _, item := range page.Items {
			var candidate struct {
				ID int `json:"candidateId"`
			}
			if json.Unmarshal(item, &candidate) != nil || candidate.ID <= 0 {
				return page, domain.Invalid("JobAdder returned an unsupported candidate record schema.")
			}
		}
		if page.Links.Next != "" {
			nextURL, nextErr := c.pageURL(page.Links.Next)
			if nextErr != nil {
				return Page{}, nextErr
			}
			if nextURL.String() == u.String() {
				return Page{}, domain.Invalid("JobAdder pagination did not advance.")
			}
			page.Links.Next = nextURL.String()
		}
		return page, nil
	}
	return page, domain.Invalid("JobAdder candidate lookup did not complete.")
}
