package jobadder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
)

const scopes = "read read_candidate offline_access"

// OAuth owns protocol requests, not secret persistence. The host must supply a
// secure credential store before enabling interactive account connection.
type OAuth struct {
	ClientID, ClientSecret, RedirectURI string
	HTTP                                *http.Client
}
type Token struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
	API       string `json:"api"`
}

func (Token) String() string { return "JobAdder token [redacted]" }

func (o OAuth) AuthorizationURL(state, verifier string) (string, error) {
	u, err := url.Parse(o.RedirectURI)
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost"))) || u.Host == "" || o.ClientID == "" || len(state) < 32 || len(verifier) < 43 || len(verifier) > 128 {
		return "", domain.Invalid("Configure the registered JobAdder redirect URI, state, and PKCE verifier.")
	}
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {o.ClientID}, "redirect_uri": {o.RedirectURI}, "scope": {scopes}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
	return "https://id.jobadder.com/connect/authorize?" + q.Encode(), nil
}

func (o OAuth) CodeFromRedirect(raw, expectedState string) (string, error) {
	u, err := url.Parse(raw)
	registered, registeredErr := url.Parse(o.RedirectURI)
	if err != nil || registeredErr != nil || u.Scheme != registered.Scheme || u.Host != registered.Host || u.Path != registered.Path || u.User != nil || u.Fragment != "" || len(expectedState) < 32 || len(u.Query()["state"]) != 1 || u.Query().Get("state") != expectedState {
		return "", domain.Invalid("JobAdder sign-in did not match this authorization request.")
	}
	if u.Query().Get("error") != "" {
		return "", domain.Invalid("JobAdder denied the sign-in request.")
	}
	if len(u.Query()["code"]) != 1 || u.Query().Get("code") == "" {
		return "", domain.Invalid("JobAdder did not return an authorization code.")
	}
	return u.Query().Get("code"), nil
}

func (o OAuth) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	if code == "" {
		return Token{}, domain.Invalid("JobAdder authorization code is missing.")
	}
	if _, err := o.AuthorizationURL(domain.ID(), verifier); err != nil {
		return Token{}, err
	}
	return o.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.RedirectURI}, "code_verifier": {verifier}})
}
func (o OAuth) Refresh(ctx context.Context, refresh string) (Token, error) {
	if refresh == "" {
		return Token{}, domain.Invalid("JobAdder refresh token is missing. Reconnect the account.")
	}
	token, err := o.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if err == nil && token.Refresh == "" {
		token.Refresh = refresh
	}
	return token, err
}
func (o OAuth) token(ctx context.Context, form url.Values) (Token, error) {
	var token Token
	if o.ClientID == "" || o.ClientSecret == "" {
		return token, domain.Invalid("Configure the registered JobAdder client ID and secret.")
	}
	form.Set("client_id", o.ClientID)
	form.Set("client_secret", o.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://id.jobadder.com/connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return token, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := http.Client{Timeout: 20 * time.Second}
	if o.HTTP != nil {
		client = *o.HTTP
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return token, domain.Invalid("JobAdder authorization could not be reached.")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return token, HTTPError{Status: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || json.Unmarshal(body, &token) != nil || token.Access == "" || token.ExpiresIn <= 0 {
		return Token{}, domain.Invalid("JobAdder returned an invalid authorization response.")
	}
	if token.API == "" {
		token.API = DefaultBase
	}
	if _, err = apiBase(token.API); err != nil {
		return Token{}, err
	}
	return token, nil
}
