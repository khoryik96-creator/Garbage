package jobadder

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestConnectPreparationFailurePreservesWorkingAccount(t *testing.T) {
	for _, reason := range []string{"busy port", "hostname mismatch", "locked vault"} {
		t.Run(reason, func(t *testing.T) {
			vault := &memoryVault{}
			c := NewConnection(vault)
			defer c.Close()
			cfg := Configuration{ClientID: "existing-client", ClientSecret: "existing-secret", RedirectURI: callbackAddress(t), Remember: true}
			if err := c.Configure(cfg); err != nil {
				t.Fatal(err)
			}
			c.record.Token = Token{Access: "working-access", Refresh: "working-refresh", API: DefaultBase}
			c.record.ExpiresAt = time.Now().Add(time.Hour)
			if err := c.persist(c.record); err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), vault.data...)
			base := "http://127.0.0.1:8000"
			switch reason {
			case "busy port":
				u, _ := url.Parse(cfg.RedirectURI)
				listener, err := net.Listen("tcp4", u.Host)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			case "hostname mismatch":
				base = "http://localhost:8000"
			case "locked vault":
				vault.fail = true
			}
			cfg.ClientID, cfg.ClientSecret = "replacement-client", "replacement-secret"
			if _, err := c.Connect(cfg, base, strings.Repeat("b", 32)); err == nil {
				t.Fatal("expected preparation failure")
			}
			if !c.Status().Connected || c.Status().Pending || c.record.ClientID != "existing-client" || !bytes.Equal(before, vault.data) {
				t.Fatal("failed preparation replaced working credentials")
			}
			vault.fail = false
			restarted := NewConnection(vault)
			defer restarted.Close()
			if !restarted.Status().Connected || restarted.record.Token.Access != "working-access" {
				t.Fatal("previous connection did not survive restart")
			}
		})
	}
}

func TestBoundDenialReturnsToSettingsAndReleasesCallback(t *testing.T) {
	vault := &memoryVault{}
	c := NewConnection(vault)
	defer c.Close()
	cfg := Configuration{ClientID: "client", ClientSecret: "secret", RedirectURI: callbackAddress(t), Remember: true}
	if err := c.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	c.record.Token = Token{Access: "previous-access", Refresh: "previous-refresh", API: DefaultBase}
	c.record.ExpiresAt = time.Now().Add(time.Hour)
	if err := c.persist(c.record); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), vault.data...)
	browser := strings.Repeat("b", 32)
	cfg.ClientSecret = "" // Settings keeps the existing secret.
	address, err := c.Connect(cfg, "http://127.0.0.1:8000", browser)
	if err != nil {
		t.Fatal(err)
	}
	authorize, _ := url.Parse(address)
	state := authorize.Query().Get("state")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u, _ := url.Parse(cfg.RedirectURI)
	jar.SetCookies(u, []*http.Cookie{{Name: "gt_jobadder_flow", Value: browser, Path: "/"}})
	for _, query := range []string{"state=wrong&error=access_denied", "state=" + state + "&error=denied&error=denied", "state=" + state + "&code=code&broken=%"} {
		r, err := client.Get(cfg.RedirectURI + "?" + query)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 403 || !c.Status().Pending {
			t.Fatal("invalid callback consumed sign-in attempt")
		}
	}
	r, err := client.Get(cfg.RedirectURI + "?state=" + state + "&error=access_denied&error_description=private-provider-message")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	status := c.Status()
	if r.StatusCode != 303 || r.Header.Get("Location") != "http://127.0.0.1:8000/settings?jobadder=failed" || status.Pending || !status.Connected || !strings.Contains(status.Error, "cancelled or denied") || strings.Contains(status.Error+string(body), "private-provider-message") || !bytes.Equal(before, vault.data) {
		t.Fatal("denial failed to return safely to the working account", status)
	}
	// Reopening the same port demonstrates that the completed callback listener
	// no longer blocks a subsequent connection attempt.
	deadline := time.Now().Add(2 * time.Second)
	for {
		listener, err := net.Listen("tcp4", u.Host)
		if err == nil {
			listener.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("denied callback retained its listener", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := c.Connect(cfg, "http://127.0.0.1:8000", browser); err != nil {
		t.Fatal("retry failed", err)
	}
	c.CancelSignIn()
	if c.Status().Pending || !c.Status().Connected || c.Status().Error != "" || !bytes.Equal(before, vault.data) {
		t.Fatal("cancel removed existing connection")
	}
}

func TestCancelDuringExchangeIsResponsiveAndCannotCommitLateToken(t *testing.T) {
	vault := &memoryVault{}
	c := NewConnection(vault)
	defer c.Close()
	cfg := Configuration{ClientID: "client", ClientSecret: "secret", RedirectURI: callbackAddress(t), Remember: true}
	if err := c.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	c.record.Token = Token{Access: "previous-access", Refresh: "previous-refresh", API: DefaultBase}
	c.record.ExpiresAt = time.Now().Add(time.Hour)
	if err := c.persist(c.record); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), vault.data...)
	started := make(chan *http.Request, 1)
	release := make(chan struct{})
	defer close(release)
	c.HTTP = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		started <- r
		// Deliberately return even after cancellation: a late provider response
		// must never win over the user's cancellation.
		<-release
		return response(200, `{"access_token":"late-access","refresh_token":"late-refresh","expires_in":3600}`), nil
	})}
	browser := strings.Repeat("b", 32)
	address, err := c.Connect(cfg, "http://127.0.0.1:8000", browser)
	if err != nil {
		t.Fatal(err)
	}
	authorize, _ := url.Parse(address)
	r := httptest.NewRequest("GET", cfg.RedirectURI+"?state="+authorize.Query().Get("state")+"&code=code", nil)
	r.AddCookie(&http.Cookie{Name: "gt_jobadder_flow", Value: browser})
	finished := make(chan struct{})
	go func() { c.callback(httptest.NewRecorder(), r); close(finished) }()
	var providerRequest *http.Request
	select {
	case providerRequest = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("exchange did not start")
	}
	cancelled := make(chan struct{})
	go func() { c.CancelSignIn(); close(cancelled) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancel blocked behind provider exchange")
	}
	select {
	case <-providerRequest.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("provider request was not cancelled")
	}
	release <- struct{}{}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("late callback did not finish")
	}
	if c.Status().Pending || !c.Status().Connected || c.record.Token.Access != "previous-access" || !bytes.Equal(before, vault.data) {
		t.Fatal("cancelled authorization committed a late token")
	}
}
