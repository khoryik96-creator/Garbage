package jobadder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestReadOnlyCandidatePaginationAndRateLimit(t *testing.T) {
	client, err := New("https://au3api.jobadder.com/v2", func(context.Context) (string, error) { return "test-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-token" || r.URL.Host != "au3api.jobadder.com" {
			t.Fatal("read request contract changed")
		}
		if calls == 1 {
			result := response(429, `{"personal":"must not escape"}`)
			result.Header.Set("Retry-After", "0")
			return result, nil
		}
		if r.URL.RawQuery == "page=2" {
			return response(200, `{"items":[],"totalCount":1,"links":{}}`), nil
		}
		return response(200, `{"items":[{"candidateId":42,"employment":{"current":{"employer":"Demo employer"}},"customFields":{"4":{"value":"AUD"}}}],"totalCount":1,"links":{"next":"?page=2"}}`), nil
	})}
	page, err := client.Candidates(context.Background(), "")
	if err != nil || len(page.Items) != 1 || calls != 2 {
		t.Fatal("read failed", err)
	}
	if !strings.Contains(string(page.Items[0]), "employment") {
		t.Fatal("public schema fields discarded")
	}
	page, err = client.Candidates(context.Background(), page.Links.Next)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("pagination failed", err)
	}
}

func TestAuthenticationNeverFollowsUntrustedLinksOrRedirects(t *testing.T) {
	for _, base := range []string{"http://api.jobadder.com/v2", "https://evil.example/v2", "https://api.jobadder.com.evil/v2", "https://user@api.jobadder.com/v2", "https://api.jobadder.com:444/v2", "https://api.jobadder.com/spa/api", "https://api.jobadder.com/v2?x=1"} {
		if _, err := New(base, func(context.Context) (string, error) { return "token", nil }); err == nil {
			t.Fatal("untrusted base accepted", base)
		}
	}
	client, _ := New(DefaultBase, func(context.Context) (string, error) { return "token", nil })
	calls := 0
	client.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		result := response(302, "private-provider-response")
		result.Header.Set("Location", "https://evil.example")
		return result, nil
	})}
	for _, next := range []string{"https://evil.example/v2/candidates", "https://au3api.jobadder.com/v2/candidates", "/spa/api/candidates", "//evil.example/v2/candidates", "https://user@api.jobadder.com/v2/candidates"} {
		if _, err := client.Candidates(context.Background(), next); err == nil {
			t.Fatal("untrusted next link accepted", next)
		}
	}
	if calls != 0 {
		t.Fatal("token sent before link validation")
	}
	if _, err := client.Candidates(context.Background(), ""); err == nil || strings.Contains(err.Error(), "private-provider-response") {
		t.Fatal("redirect accepted or provider text leaked")
	}
	if calls != 1 {
		t.Fatal("followed a redirect")
	}
}

func TestInvalidRecordsAndBoundedRetry(t *testing.T) {
	client, _ := New(DefaultBase, func(context.Context) (string, error) { return "token", nil })
	for _, body := range []string{`{}`, `{"items":[{"id":42}],"totalCount":1}`, `{"items":[{"candidateId":0}],"totalCount":1}`, `{"items":[],"totalCount":1,"links":{"next":"https://api.jobadder.com/v2/candidates"}}`} {
		client.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}
		if _, err := client.Candidates(context.Background(), ""); err == nil {
			t.Fatal("invalid schema accepted", body)
		}
	}
	client.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		r := response(429, "")
		r.Header.Set("Retry-After", "60")
		return r, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Candidates(ctx, ""); err == nil {
		t.Fatal("rate-limit wait ignored cancellation")
	}
	if retryAfter("9223372036854775807") != time.Duration(math.MaxInt64/int64(time.Second))*time.Second {
		t.Fatal("retry duration overflow")
	}
}

func TestOAuthStatePKCEExchangeAndRefresh(t *testing.T) {
	oauth := OAuth{ClientID: "test-client", ClientSecret: "test-secret", RedirectURI: "http://127.0.0.1:8765/jobadder/callback"}
	state, verifier := strings.Repeat("a", 32), strings.Repeat("b", 43)
	address, err := oauth.AuthorizationURL(state, verifier)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	if u.Host != "id.jobadder.com" || u.Query().Get("scope") != "read read_candidate offline_access" || u.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("authorization scopes or PKCE changed")
	}
	for _, redirect := range []string{oauth.RedirectURI + "?state=wrong&code=123", "https://evil.example/callback?state=" + state + "&code=123", oauth.RedirectURI + "?state=" + state + "&state=" + state + "&code=123"} {
		if _, err := oauth.CodeFromRedirect(redirect, state); err == nil {
			t.Fatal("invalid OAuth callback accepted")
		}
	}
	code, err := oauth.CodeFromRedirect(oauth.RedirectURI+"?state="+state+"&code=123", state)
	if err != nil || code != "123" {
		t.Fatal("code binding failed", err)
	}
	oauth.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://id.jobadder.com/connect/token" || r.Method != "POST" {
			t.Fatal("token destination changed")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_secret") != "test-secret" {
			t.Fatal("client authentication absent")
		}
		if r.Form.Get("grant_type") == "authorization_code" && r.Form.Get("code_verifier") != verifier {
			t.Fatal("PKCE absent")
		}
		return response(200, `{"access_token":"new-access","expires_in":3600,"api":"https://au3api.jobadder.com/v2"}`), nil
	})}
	token, err := oauth.Exchange(context.Background(), code, verifier)
	if err != nil || token.API != "https://au3api.jobadder.com/v2" {
		t.Fatal("token exchange failed", err)
	}
	if strings.Contains(fmt.Sprint(token), "new-access") {
		t.Fatal("token formatting exposed credentials")
	}
	token, err = oauth.Refresh(context.Background(), "old-refresh")
	if err != nil || token.Refresh != "old-refresh" {
		t.Fatal("refresh token was lost", err)
	}
	oauth.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"access_token":"test","expires_in":3600,"api":"https://evil.example/v2"}`), nil
	})}
	if _, err := oauth.Refresh(context.Background(), "old-refresh"); err == nil {
		t.Fatal("untrusted token API base accepted")
	}
}

func TestRelativePaginationSelfLoopsRejected(t *testing.T) {
	client, _ := New(DefaultBase, func(context.Context) (string, error) { return "token", nil })
	for _, next := range []string{"?page=2", "/v2/candidates?page=2", DefaultBase + "/candidates?page=2"} {
		client.HTTP = &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"items":[],"totalCount":0,"links":{"next":"`+next+`"}}`), nil
		})}
		if _, err := client.Candidates(context.Background(), "?page=2"); err == nil {
			t.Fatal("pagination self-loop accepted", next)
		}
	}
}

func TestRateLimitPreservesDelayWithoutEarlyRetry(t *testing.T) {
	for _, header := range []string{"3600", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)} {
		client, _ := New(DefaultBase, func(context.Context) (string, error) { return "token", nil })
		calls := 0
		client.HTTP = &http.Client{Timeout: time.Second, Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			r := response(429, "")
			r.Header.Set("Retry-After", header)
			return r, nil
		})}
		start := time.Now()
		_, err := client.Candidates(context.Background(), "")
		var limit HTTPError
		if !errors.As(err, &limit) || limit.Status != 429 || limit.RetryAfter < 59*time.Minute || calls != 1 || time.Since(start) > time.Second {
			t.Fatalf("lost provider delay or retried early: error=%v delay=%v calls=%d", err, limit.RetryAfter, calls)
		}
	}
}

func TestOAuthConfidentialCompatibilityMode(t *testing.T) {
	oauth := OAuth{ClientID: "client", ClientSecret: "secret", RedirectURI: "http://localhost/callback", DisablePKCE: true}
	state := strings.Repeat("s", 32)
	address, err := oauth.AuthorizationURL(state, "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	if u.Query().Has("code_challenge") || u.Query().Has("code_challenge_method") || u.Query().Get("state") != state {
		t.Fatal("compatibility authorization contract changed")
	}
	if _, err = oauth.CodeFromRedirect(oauth.RedirectURI+"?state=wrong&code=code", state); err == nil {
		t.Fatal("state protection disabled")
	}
	calls := 0
	oauth.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Has("code_verifier") {
			return response(400, ""), nil
		}
		if r.Form.Get("client_secret") != "secret" || r.Form.Get("code") != "fresh-code" {
			t.Fatal("confidential authentication missing")
		}
		return response(200, `{"access_token":"access","expires_in":3600}`), nil
	})}
	if _, err = oauth.Exchange(context.Background(), "fresh-code", ""); err != nil || calls != 1 {
		t.Fatal("reference provider rejected exchange", err, calls)
	}
	if _, err = oauth.AuthorizationURL(state, strings.Repeat("v", 43)); err == nil {
		t.Fatal("ambiguous PKCE mode accepted")
	}
	oauth.ClientSecret = ""
	if _, err = oauth.AuthorizationURL(state, ""); err == nil {
		t.Fatal("public client allowed to disable PKCE")
	}
	oauth.DisablePKCE = false
	if _, err = oauth.AuthorizationURL(state, ""); err == nil {
		t.Fatal("default PKCE protection disabled")
	}
}
