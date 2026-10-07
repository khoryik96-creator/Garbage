package jobadder

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultRedirect = "http://127.0.0.1:8765/jobadder/callback"

type Configuration struct {
	ClientID, ClientSecret, RedirectURI string
	PKCE                                bool
	Remember                            bool
}
type credentialRecord struct {
	Configuration
	Token     Token
	ExpiresAt time.Time
}
type ConnectionStatus struct {
	Configured, Connected, Remember, SecureStorage, PKCE, Saved bool
	ClientID, RedirectURI, Error                                string
}
type authorization struct {
	state, verifier, browser, returnBase string
	expires                              time.Time
}
type Connection struct {
	mu      sync.Mutex
	vault   Vault
	record  credentialRecord
	pending *authorization
	server  *http.Server
	HTTP    *http.Client
	failure string
	dirty   bool
	saved   bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewConnection(vault Vault) *Connection {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Connection{vault: vault, ctx: ctx, cancel: cancel}
	data, err := vault.Load()
	c.saved = err == nil || !errors.Is(err, ErrNoCredentials)
	if err != nil && !errors.Is(err, ErrNoCredentials) {
		c.failure = "Protected connection details could not be loaded. Unlock your OS keyring or enter them again."
	}
	if err == nil {
		if json.Unmarshal(data, &c.record) != nil || validateConfiguration(c.record.Configuration) != nil {
			c.record = credentialRecord{}
			c.failure = "Saved connection details could not be loaded. Enter them again."
		}
	}

	return c
}
func validateConfiguration(cfg Configuration) error {
	u, err := url.Parse(cfg.RedirectURI)
	port := 0
	if err == nil {
		port, _ = strconv.Atoi(u.Port())
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || len(cfg.ClientID) > 512 || len(cfg.ClientSecret) > 4096 || err != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") || port < 1024 || port > 65535 || u.Path != "/jobadder/callback" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return domain.Invalid("Enter the Client ID, Client Secret and registered loopback callback URL, for example " + DefaultRedirect + ".")
	}
	return nil
}
func (c *Connection) Status() ConnectionStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.record
	redirect := r.RedirectURI
	if redirect == "" {
		redirect = DefaultRedirect
	}
	return ConnectionStatus{Saved: c.saved, Configured: r.ClientID != "", Connected: r.Token.Access != "" && (time.Now().Before(r.ExpiresAt) || r.Token.Refresh != ""), Remember: r.Remember, SecureStorage: c.vault.Available(), PKCE: r.PKCE, ClientID: r.ClientID, RedirectURI: redirect, Error: c.failure}
}
func (c *Connection) persist(record credentialRecord) error {
	if !record.Remember {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if !c.vault.Available() || c.vault.Save(data) != nil {
		return domain.Invalid("Secure credential storage is unavailable or locked. Unlock your OS keyring, or use this connection for this session only.")
	}
	c.saved = true
	return nil
}
func (c *Connection) Configure(cfg Configuration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.RedirectURI = strings.TrimSpace(cfg.RedirectURI)
	if cfg.ClientSecret == "" && cfg.ClientID == c.record.ClientID {
		cfg.ClientSecret = c.record.ClientSecret
	}
	if err := validateConfiguration(cfg); err != nil {
		return err
	}
	record := credentialRecord{Configuration: cfg}
	if err := c.persist(record); err != nil {
		return err
	}
	if !cfg.Remember && (c.record.Remember || c.saved) {
		if err := c.vault.Delete(); err != nil {
			return domain.Invalid("The saved credentials could not be removed. Unlock your keyring and retry.")
		}
	}
	c.record = record
	if !cfg.Remember {
		c.saved = false
	}
	c.dirty = false
	c.failure = ""
	c.pending = nil
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
	return nil
}
func (c *Connection) oauth() OAuth {
	return OAuth{ClientID: c.record.ClientID, ClientSecret: c.record.ClientSecret, RedirectURI: c.record.RedirectURI, DisablePKCE: !c.record.PKCE, HTTP: c.HTTP}
}
func (c *Connection) Start(returnBase, browser string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateConfiguration(c.record.Configuration); err != nil {
		return "", err
	}
	if len(browser) != 32 {
		return "", domain.Invalid("Start authorization from this browser session.")
	}
	base, err := url.Parse(returnBase)
	if err != nil || base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" {
		return "", domain.Invalid("Open Settings from the local app to connect.")
	}
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
	callback, _ := url.Parse(c.record.RedirectURI)
	if callback.Hostname() != base.Hostname() {
		return "", domain.Invalid("Use the same loopback hostname in Settings and the registered callback URL (127.0.0.1 or localhost).")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", callback.Port()))
	if err != nil {
		return "", domain.Invalid("The callback port is in use. Close another connection attempt or choose a different registered callback port.")
	}
	state := domain.ID() + domain.ID()
	verifier := ""
	if c.record.PKCE {
		data := make([]byte, 32)
		if _, err = rand.Read(data); err != nil {
			listener.Close()
			return "", err
		}
		verifier = base64.RawURLEncoding.EncodeToString(data)
	}
	address, err := c.oauth().AuthorizationURL(state, verifier)
	if err != nil {
		listener.Close()
		return "", err
	}
	pending := &authorization{state: state, verifier: verifier, browser: browser, returnBase: returnBase, expires: time.Now().Add(10 * time.Minute)}
	c.pending = pending
	server := &http.Server{Handler: http.HandlerFunc(c.callback), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	c.server = server
	go server.Serve(listener)
	go func() {
		timer := time.NewTimer(10 * time.Minute)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-c.ctx.Done():
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.pending == pending {
			c.pending = nil
			server.Close()
			if c.server == server {
				c.server = nil
			}
		}
	}()
	return address, nil
}
func (c *Connection) callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	c.mu.Lock()
	defer c.mu.Unlock()
	pending := c.pending
	cookie, err := r.Cookie("gt_jobadder_flow")
	registered, _ := url.Parse(c.record.RedirectURI)
	if pending == nil || time.Now().After(pending.expires) || r.Method != "GET" || registered == nil || r.Host != registered.Host || r.URL.Path != registered.Path || err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(pending.browser)) != 1 {
		http.Error(w, "This sign-in attempt is invalid or expired. Start again from Settings.", 403)
		return
	}
	code, err := c.oauth().CodeFromRedirect(c.record.RedirectURI+"?"+r.URL.RawQuery, pending.state)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	c.pending = nil
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(r.Context(), cancel)
	defer stop()
	token, err := c.oauth().Exchange(ctx, code, pending.verifier)
	if err == nil {
		record := c.record
		record.Token = token
		record.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		c.record = record
		err = c.persist(record)
		c.dirty = err != nil
		if err == nil {
			c.failure = ""
		}
	}
	result := "connected"
	if err != nil {
		c.failure = err.Error()
		result = "failed"
	}
	http.SetCookie(w, &http.Cookie{Name: "gt_jobadder_flow", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, pending.returnBase+"/settings?jobadder="+result, 303)
	// Close only after the callback response has been sent. HTTP server shutdown
	// runs outside this handler/lock and also cancels the pending listener.
	if c.server != nil {
		server := c.server
		c.server = nil
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			server.Shutdown(ctx)
		}()
	}
}
func (c *Connection) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.record.Remember || c.saved {
		if err := c.vault.Delete(); err != nil {
			return domain.Invalid("Saved credentials could not be removed. Unlock your OS keyring and retry.")
		}
	}
	c.record = credentialRecord{}
	c.saved = false
	c.dirty = false
	c.pending = nil
	c.failure = ""
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
	return nil
}
func (c *Connection) Close() {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
}
func (c *Connection) accessToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirty {
		if err := c.persist(c.record); err != nil {
			c.failure = err.Error()
			return "", err
		}
		c.dirty = false
		c.failure = ""
	}
	if c.record.Token.Access == "" {
		return "", domain.Invalid("Connect JobAdder in Settings first.")
	}
	if time.Now().Add(30 * time.Second).Before(c.record.ExpiresAt) {
		return c.record.Token.Access, nil
	}
	token, err := c.oauth().Refresh(ctx, c.record.Token.Refresh)
	if err != nil {
		c.failure = err.Error()
		return "", err
	}
	if !token.APIReported {
		token.API = c.record.Token.API
	}
	record := c.record
	record.Token = token
	record.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	c.record = record
	if err = c.persist(record); err != nil {
		c.dirty = true
		c.failure = err.Error()
		return "", err
	}
	c.dirty = false
	c.failure = ""
	return token.Access, nil
}
func (c *Connection) Candidates(ctx context.Context, next string) (Page, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	if _, err := c.accessToken(ctx); err != nil {
		return Page{}, err
	}
	c.mu.Lock()
	base := c.record.Token.API
	clientHTTP := c.HTTP
	c.mu.Unlock()
	client, err := New(base, c.accessToken)
	if err != nil {
		return Page{}, err
	}
	if clientHTTP != nil {
		client.HTTP = clientHTTP
	}
	page, err := client.Candidates(ctx, next)
	if err != nil {
		var failure HTTPError
		if errors.As(err, &failure) && (failure.Status == 401 || failure.Status == 403) {
			c.mu.Lock()
			c.failure = failure.Error()
			c.mu.Unlock()
		}
	}
	return page, err
}
