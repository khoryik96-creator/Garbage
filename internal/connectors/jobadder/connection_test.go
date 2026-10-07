package jobadder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

type memoryVault struct {
	data []byte
	fail bool
}

func (v *memoryVault) Available() bool { return !v.fail }
func (v *memoryVault) Load() ([]byte, error) {
	if len(v.data) == 0 {
		return nil, ErrNoCredentials
	}
	return append([]byte(nil), v.data...), nil
}
func (v *memoryVault) Save(data []byte) error {
	if v.fail {
		return errors.New("vault locked")
	}
	v.data = append([]byte(nil), data...)
	return nil
}
func (v *memoryVault) Delete() error { v.data = nil; return nil }
func callbackAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + l.Addr().String() + "/jobadder/callback"
	l.Close()
	return address
}
func TestConnectionAuthorizationPersistenceRefreshAndDisconnect(t *testing.T) {
	vault := &memoryVault{}
	c := NewConnection(vault)
	defer c.Close()
	redirect := callbackAddress(t)
	cfg := Configuration{ClientID: "client", ClientSecret: "secret-never-rendered", RedirectURI: redirect, Remember: true}
	if err := c.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	var exchanges, refreshes int
	c.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "id.jobadder.com" {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_secret") != cfg.ClientSecret || r.Form.Has("code_verifier") {
				t.Fatal("confidential client contract changed")
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes++
				return response(200, `{"access_token":"refreshed","refresh_token":"rotated","expires_in":3600}`), nil
			}
			exchanges++
			return response(200, `{"access_token":"access","refresh_token":"refresh","expires_in":3600,"api":"https://au3api.jobadder.com/v2"}`), nil
		}
		if r.URL.Host != "au3api.jobadder.com" || r.Header.Get("Authorization") != "Bearer refreshed" {
			t.Fatal("region or refreshed token lost")
		}
		return response(200, `{"items":[{"candidateId":42,"firstName":"Real","lastName":"Candidate"}],"totalCount":1,"links":{}}`), nil
	})}
	browser := strings.Repeat("b", 32)
	address, err := c.Start("http://127.0.0.1:8000", browser)
	if err != nil {
		t.Fatal(err)
	}
	authorize, _ := url.Parse(address)
	state := authorize.Query().Get("state")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u, _ := url.Parse(redirect)
	jar.SetCookies(u, []*http.Cookie{{Name: "gt_jobadder_flow", Value: browser, Path: "/"}})
	for _, suffix := range []string{"?state=wrong&code=code", "?state=" + state + "&state=" + state + "&code=code"} {
		r, err := client.Get(redirect + suffix)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 403 {
			t.Fatal("invalid callback accepted")
		}
	}
	r, err := client.Get(redirect + "?state=" + state + "&code=fresh-code")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 303 || !c.Status().Connected || exchanges != 1 || strings.Contains(string(body), cfg.ClientSecret) {
		t.Fatal("connection failed or secret exposed", r.StatusCode)
	}
	reloaded := NewConnection(vault)
	defer reloaded.Close()
	if !reloaded.Status().Connected {
		t.Fatal("saved connection did not survive restart")
	}
	reloaded.HTTP = c.HTTP
	reloaded.record.ExpiresAt = time.Now().Add(-time.Minute)
	page, err := reloaded.Candidates(context.Background(), "")
	if err != nil || len(page.Items) != 1 || refreshes != 1 {
		t.Fatal("refresh/read failed", err)
	}
	if reloaded.record.Token.API != "https://au3api.jobadder.com/v2" || reloaded.record.Token.Refresh != "rotated" {
		t.Fatal("refresh lost regional API or rotated token")
	}
	if err = reloaded.Disconnect(); err != nil || len(vault.data) != 0 || reloaded.Status().Connected {
		t.Fatal("disconnect retained credentials", err)
	}
	if _, err = reloaded.Candidates(context.Background(), ""); err == nil {
		t.Fatal("disconnected read permitted")
	}
	var decoded map[string]any
	if err = json.Unmarshal(body, &decoded); err == nil {
		t.Fatal("callback unexpectedly returned token JSON")
	}
}
func TestConnectionSecureStorageAndSessionMode(t *testing.T) {
	v := &memoryVault{fail: true}
	c := NewConnection(v)
	defer c.Close()
	cfg := Configuration{ClientID: "client", ClientSecret: "secret", RedirectURI: callbackAddress(t), Remember: true}
	if err := c.Configure(cfg); err == nil || c.Status().Configured {
		t.Fatal("locked vault silently accepted persistent credentials")
	}
	cfg.Remember = false
	if err := c.Configure(cfg); err != nil || !c.Status().Configured || len(v.data) != 0 {
		t.Fatal("explicit session-only use failed", err)
	}
	if _, err := c.Start("http://127.0.0.1:8000", "short"); err == nil {
		t.Fatal("weak browser binding accepted")
	}
	if _, err := c.Start("http://localhost:8000", strings.Repeat("b", 32)); err == nil {
		t.Fatal("callback cookie host mismatch accepted")
	}
	if strings.Contains(c.Status().ClientID, "secret") {
		t.Fatal("status exposed secrets")
	}
}

func TestRotatedRefreshTokenSurvivesTemporaryVaultFailure(t *testing.T) {
	v := &memoryVault{}
	c := NewConnection(v)
	defer c.Close()
	if err := c.Configure(Configuration{ClientID: "client", ClientSecret: "secret", RedirectURI: callbackAddress(t), Remember: true}); err != nil {
		t.Fatal(err)
	}
	c.record.Token = Token{Access: "old", Refresh: "consumed-once", API: "https://au3api.jobadder.com/v2"}
	c.record.ExpiresAt = time.Now().Add(-time.Minute)
	calls := 0
	c.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`), nil
	})}
	v.fail = true
	if _, err := c.accessToken(context.Background()); err == nil {
		t.Fatal("locked storage was hidden")
	}
	if c.record.Token.Refresh != "rotated" || !c.dirty || c.Status().Error == "" {
		t.Fatal("rotated credentials were discarded")
	}
	v.fail = false
	token, err := c.accessToken(context.Background())
	if err != nil || token != "new" || calls != 1 || c.dirty {
		t.Fatal("retried consumed refresh token rather than persistence", err, calls)
	}
	reloaded := NewConnection(v)
	defer reloaded.Close()
	if reloaded.record.Token.Refresh != "rotated" {
		t.Fatal("new refresh token not persisted after unlock")
	}
}

func TestInvalidSavedCredentialsAreRemovedForSessionMode(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		v := &memoryVault{data: []byte("invalid encrypted record after successful vault read")}
		c := NewConnection(v)
		if !c.Status().Saved || c.Status().Error == "" {
			t.Fatal("invalid saved details were hidden")
		}
		if disconnect {
			if err := c.Disconnect(); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := c.Configure(Configuration{ClientID: "client", ClientSecret: "secret", RedirectURI: callbackAddress(t)}); err != nil {
				t.Fatal(err)
			}
		}
		if len(v.data) != 0 || c.Status().Saved {
			t.Fatal("old saved record survived explicit removal")
		}
		c.Close()
	}
}
