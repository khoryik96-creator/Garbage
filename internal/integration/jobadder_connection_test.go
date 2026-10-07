package integration

import (
	"encoding/json"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/connectors/jobadder"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

type connectionVault struct {
	mu   sync.Mutex
	data []byte
}

func (v *connectionVault) Available() bool { return true }
func (v *connectionVault) Load() ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.data) == 0 {
		return nil, jobadder.ErrNoCredentials
	}
	return append([]byte(nil), v.data...), nil
}
func (v *connectionVault) Save(data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data = append([]byte(nil), data...)
	return nil
}
func (v *connectionVault) Delete() error { v.mu.Lock(); defer v.mu.Unlock(); v.data = nil; return nil }

type providerTransport func(*http.Request) (*http.Response, error)

func (f providerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func providerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestJobAdderSettingsCallbackBrowseRestartAndDisconnect(t *testing.T) {
	s, _, _ := setup(t)
	vault := &connectionVault{}
	connection := jobadder.NewConnection(vault)
	defer connection.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	must(t, err)
	callback := "http://" + listener.Addr().String() + "/jobadder/callback"
	must(t, listener.Close())
	var reads, exchanges atomic.Int32
	provider := &http.Client{Transport: providerTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "id.jobadder.com" {
			exchanges.Add(1)
			if r.Method != "POST" {
				t.Error("wrong token method")
			}
			must(t, r.ParseForm())
			if r.PostForm.Get("client_secret") != "secret-never-exposed" || r.PostForm.Get("redirect_uri") != callback {
				t.Error("token request contract incorrect")
			}
			return providerResponse(200, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600,"api":"https://au3api.jobadder.com/v2"}`), nil
		}
		reads.Add(1)
		if r.Method != "GET" || r.URL.Host != "au3api.jobadder.com" || r.Header.Get("Authorization") != "Bearer private-access" {
			t.Error("wrong candidate request")
		}
		if r.URL.Query().Get("page") == "2" {
			return providerResponse(200, `{"items":[{"candidateId":43,"firstName":"Second","lastName":"Person","address":{"country":"Malaysia","countryCode":"MY"}}],"totalCount":2,"links":{}}`), nil
		}
		return providerResponse(200, `{"items":[{"candidateId":42,"firstName":"Connected","lastName":"Person","email":"real@example.invalid","mobile":"+61 412 000 111","address":{"country":null,"countryCode":"AU"}}],"totalCount":2,"links":{"next":"?page=2"}}`), nil
	})}
	connection.HTTP = provider
	handler, err := web.NewWithOptions(s, false, web.Options{JobAdder: connection})
	must(t, err)
	host := httptest.NewServer(handler)
	defer host.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	read := func(address string) (int, string, http.Header) {
		t.Helper()
		r, err := browser.Get(address)
		must(t, err)
		body, err := io.ReadAll(r.Body)
		must(t, err)
		must(t, r.Body.Close())
		for _, secret := range []string{"secret-never-exposed", "private-access", "private-refresh"} {
			if strings.Contains(string(body), secret) {
				t.Fatal("secret leaked into browser response")
			}
		}
		return r.StatusCode, html.UnescapeString(string(body)), r.Header
	}
	status, _, _ := read(host.URL + "/settings")
	if status != 200 {
		t.Fatal("settings inaccessible")
	}
	u, _ := url.Parse(host.URL)
	csrf := ""
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "gt_csrf" {
			csrf = cookie.Value
		}
	}
	form := url.Values{"client_id": {"client"}, "client_secret": {"secret-never-exposed"}, "redirect_uri": {callback}, "remember": {"1"}, "_csrf": {csrf}}
	post := func(path string, values url.Values) *http.Response {
		t.Helper()
		r, err := browser.PostForm(host.URL+path, values)
		must(t, err)
		defer r.Body.Close()
		return r
	}
	unauthorized := post("/jobadder/connect", url.Values{"client_id": {"client"}})
	if unauthorized.StatusCode != 403 || exchanges.Load() != 0 {
		t.Fatal("CSRF protection missing")
	}
	// An occupied callback port must not save a half-configured connection.
	callbackURLParts, _ := url.Parse(callback)
	occupied, err := net.Listen("tcp4", callbackURLParts.Host)
	must(t, err)
	blocked := post("/jobadder/connect", form)
	must(t, occupied.Close())
	if blocked.StatusCode != http.StatusUnprocessableEntity || connection.Status().Configured || connection.Status().Saved {
		t.Fatal("failed connect persisted details", blocked.StatusCode)
	}
	response := post("/jobadder/connect", form)
	if response.StatusCode != 303 {
		t.Fatal("connect form failed", response.StatusCode)
	}
	authorize, err := url.Parse(response.Header.Get("Location"))
	must(t, err)
	if authorize.Host != "id.jobadder.com" || authorize.Query().Get("redirect_uri") != callback {
		t.Fatal("wrong authorization redirect")
	}
	status, body, _ := read(host.URL + "/settings")
	if status != 200 || !strings.Contains(body, "Waiting for JobAdder sign-in") || !connection.Status().Pending {
		t.Fatal("pending sign-in not visible")
	}
	response = post("/jobadder/cancel", url.Values{"_csrf": {csrf}})
	if response.StatusCode != 303 || connection.Status().Pending || !connection.Status().Configured {
		t.Fatal("cancel failed or removed configured details")
	}
	form.Set("client_secret", "")
	response = post("/jobadder/connect", form)
	if response.StatusCode != 303 {
		t.Fatal("retry with retained secret failed", response.StatusCode)
	}
	authorize, err = url.Parse(response.Header.Get("Location"))
	must(t, err)
	callbackURL := callback + "?state=" + authorize.Query().Get("state") + "&code=single-use-code"
	stranger := &http.Client{}
	wrong, err := stranger.Get(callbackURL)
	must(t, err)
	must(t, wrong.Body.Close())
	if wrong.StatusCode != 403 || exchanges.Load() != 0 {
		t.Fatal("callback accepted in another browser")
	}
	status, _, headers := read(callbackURL)
	if status != 303 || headers.Get("Location") != host.URL+"/settings?jobadder=connected" {
		t.Fatal("callback did not return to Settings", status)
	}
	status, body, _ = read(host.URL + "/settings")
	if status != 200 || !strings.Contains(body, "Browse JobAdder profiles") {
		t.Fatal("connected state missing")
	}
	status, body, _ = read(host.URL + "/jobadder/profiles")
	if status != 200 {
		t.Fatal("live browse failed", status)
	}
	for _, value := range []string{"Connected Person", "real@example.invalid", "+61 412 000 111", "Australia", "JobAdder data"} {
		if !strings.Contains(body, value) {
			t.Errorf("live browse omitted %q", value)
		}
	}
	status, body, _ = read(host.URL + "/jobadder/profiles?next=" + url.QueryEscape("https://au3api.jobadder.com/v2/candidates?page=2"))
	if status != 200 || !strings.Contains(body, "Second Person") {
		t.Fatal("pagination failed")
	}
	restarted := jobadder.NewConnection(vault)
	defer restarted.Close()
	restarted.HTTP = provider
	if !restarted.Status().Connected {
		t.Fatal("remembered connection lost on restart")
	}
	count := 0
	must(t, s.DB.QueryRow("SELECT COUNT(*) FROM demo_candidates").Scan(&count))
	if count != 10 {
		t.Fatal("live browse changed the local workspace")
	}
	response = post("/jobadder/disconnect", url.Values{"_csrf": {csrf}})
	if response.StatusCode != 303 || connection.Status().Connected {
		t.Fatal("disconnect failed")
	}
	reloaded := jobadder.NewConnection(vault)
	defer reloaded.Close()
	if reloaded.Status().Configured {
		t.Fatal("saved credentials survived disconnect")
	}
	var record map[string]any
	data, _ := vault.Load()
	if json.Unmarshal(data, &record) == nil {
		t.Fatal("vault was not cleared")
	}
	if exchanges.Load() != 1 || reads.Load() != 2 {
		t.Fatal("unexpected provider requests", exchanges.Load(), reads.Load())
	}
}
