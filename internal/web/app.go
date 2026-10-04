package web

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/audit"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/policy"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

//go:embed templates/*.html static/* openapi.json
var assets embed.FS

type App struct {
	Store          *storage.Store
	Review         *audit.Review
	Templates      *template.Template
	EmbeddedWorker bool
	Options        Options
}

// Options describes the host; installed and developer workspaces share the same UI.
type Options struct {
	Version         string
	DatabasePath    string
	DocumentWorker  bool
	DesktopInstance string
	DesktopReady    func() bool
	SigningStatus   string
	Shutdown        func()
}

func New(s *storage.Store, embedded bool) (http.Handler, error) {
	return NewWithOptions(s, embedded, Options{})
}
func NewWithOptions(s *storage.Store, embedded bool, options Options) (http.Handler, error) {
	functions := template.FuncMap{"prefix": strings.HasPrefix, "short": func(s string) string { return s[:min(8, len(s))] }, "shortPointer": func(s *string) string {
		if s == nil {
			return ""
		}
		return (*s)[:min(8, len(*s))]
	}, "title": func(s string) string {
		words := strings.Fields(strings.ReplaceAll(s, "_", " "))
		for i, w := range words {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
		return strings.Join(words, " ")
	}, "timestamp": func(v float64) string {
		return time.Unix(int64(v), 0).In(time.FixedZone("MYT", 8*3600)).Format("02 Jan 2006 · 15:04 MYT")
	}, "country": policy.Name, "countryCode": func(v string) string { return policy.Name(&v) }, "emptyValue": func(v *string) string {
		if domain.Empty(v) {
			return "Empty"
		}
		return *v
	}, "selected": func(v *string, code string) bool { return v != nil && *v == code }, "maximum": func(n int) int { return max(n, 1) }, "jsonPretty": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }, "actions": func(state string) []string {
		switch state {
		case "queued", "running":
			return []string{"pause", "cancel"}
		case "paused", "failed":
			return []string{"resume", "cancel"}
		}
		return nil
	}}
	templates, err := template.New("pages").Funcs(functions).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	a := &App{Store: s, Review: audit.New(s), Templates: templates, EmbeddedWorker: embedded, Options: options}
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/gaps", a.gaps)
	mux.HandleFunc("GET /api/fields", a.fields)
	mux.HandleFunc("GET /api/docs", func(w http.ResponseWriter, r *http.Request) { a.render(w, r, "docs", map[string]any{}, 200) })
	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.ReadFile("openapi.json")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/candidates", a.candidates)
	mux.HandleFunc("GET /api/runs", a.runs)
	mux.HandleFunc("POST /api/runs", a.newRun)
	mux.HandleFunc("GET /api/runs/{id}", a.run)
	mux.HandleFunc("POST /api/runs/{id}/{action}", a.changeRun)
	mux.HandleFunc("GET /api/runs/{id}/suggestions", a.suggestions)
	mux.HandleFunc("GET /api/runs/{id}/writebacks", a.writes)
	mux.HandleFunc("POST /api/suggestions/{id}/approve", a.approve)
	mux.HandleFunc("POST /api/suggestions/{id}/reject", a.reject)
	mux.HandleFunc("POST /api/writebacks/{id}/undo", a.undo)
	mux.HandleFunc("POST /api/demo/candidates/{id}/country", a.edit)
	mux.HandleFunc("GET /{$}", a.dashboard)
	mux.HandleFunc("GET /profiles", a.profiles)
	mux.HandleFunc("GET /audit", a.auditPage)
	mux.HandleFunc("GET /runs", a.runsPage)
	mux.HandleFunc("GET /settings", a.settingsPage)
	mux.HandleFunc("POST /workspace/backup", a.backupWorkspace)
	mux.HandleFunc("POST /workspace/restore", a.restoreWorkspace)
	if options.DesktopInstance != "" && options.Shutdown != nil {
		var stopping atomic.Bool
		mux.HandleFunc("GET /api/desktop/instance", func(w http.ResponseWriter, r *http.Request) {
			if stopping.Load() || (options.DesktopReady != nil && !options.DesktopReady()) {
				jsonResponse(w, 503, map[string]string{"status": "saving"})
				return
			}
			jsonResponse(w, 200, map[string]string{"instance_id": options.DesktopInstance})
		})
		mux.HandleFunc("POST /desktop/quit", func(w http.ResponseWriter, r *http.Request) {
			if err := formKeys(r, "_csrf"); err != nil {
				a.fail(w, r, err, 0)
				return
			}
			stopping.Store(true)
			nonce := domain.ID()
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'nonce-"+nonce+"'; frame-ancestors 'none'; base-uri 'none'")
			a.render(w, r, "closed", map[string]any{"ClosedNonce": nonce}, 200)
			go options.Shutdown()
		})
	}
	mux.HandleFunc("GET /runs/{id}", a.runPage)
	mux.HandleFunc("POST /runs", a.newRun)
	mux.HandleFunc("POST /runs/{id}/{action}", a.changeRun)
	mux.HandleFunc("POST /suggestions/{id}/approve", a.approve)
	mux.HandleFunc("POST /suggestions/{id}/reject", a.reject)
	mux.HandleFunc("POST /writebacks/{id}/undo", a.undo)
	return a.secure(mux), nil
}
func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "same-origin")
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "[::1]" {
			http.Error(w, "Unrecognized Host.", 400)
			return
		}
		limit := int64(1 << 20)
		if r.URL.Path == "/workspace/restore" {
			limit = maxRestoreBytes + (1 << 20)
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		cookie, err := r.Cookie("gt_csrf")
		token := ""
		if err == nil {
			token = cookie.Value
		}
		if len(token) != 32 {
			token = domain.ID()
		}
		http.SetCookie(w, &http.Cookie{Name: "gt_csrf", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if err != nil || len(cookie.Value) != 32 {
				a.fail(w, r, domain.Invalid("Refresh the page before submitting changes."), 403)
				return
			}
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if origin := r.Header.Get("Origin"); origin != "" && strings.TrimSuffix(origin, "/") != scheme+"://"+r.Host {
				a.fail(w, r, domain.Invalid("Cross-origin changes are not allowed."), 403)
				return
			}
			supplied := r.Header.Get("X-CSRF-Token")
			contentType := r.Header.Get("Content-Type")
			if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || strings.HasPrefix(contentType, "multipart/form-data") {
				parseErr := r.ParseForm()
				if strings.HasPrefix(contentType, "multipart/form-data") {
					parseErr = r.ParseMultipartForm(1 << 20)
					if r.MultipartForm != nil {
						defer r.MultipartForm.RemoveAll()
					}
				}
				if parseErr != nil {
					a.fail(w, r, domain.Invalid("Invalid form."), 422)
					return
				}
				if supplied == "" {
					supplied = r.PostForm.Get("_csrf")
				}
			}
			if err != nil || supplied == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(supplied)) != 1 {
				a.fail(w, r, domain.Invalid("Refresh the page before submitting changes."), 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return domain.Invalid("Send an application/json body.")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return domain.Invalid("Invalid or oversized request.")
	}
	check := json.NewDecoder(bytes.NewReader(body))
	start, err := check.Token()
	if err != nil || start != json.Delim('{') {
		return domain.Invalid("Send one JSON object.")
	}
	keys := map[string]bool{}
	for check.More() {
		token, err := check.Token()
		if err != nil {
			return domain.Invalid("Invalid request body.")
		}
		key, ok := token.(string)
		if !ok || keys[key] {
			return domain.Invalid("Duplicate or invalid request field.")
		}
		keys[key] = true
		var value json.RawMessage
		if err = check.Decode(&value); err != nil {
			return domain.Invalid("Invalid request body.")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && key != "value" && key != "country" {
			return domain.Invalid("Unsupported null field.")
		}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return domain.Invalid("Invalid request body or unsupported fields.")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return domain.Invalid("Send one JSON object.")
	}
	return nil
}
func (a *App) fail(w http.ResponseWriter, r *http.Request, err error, status int) {
	if status == 0 {
		var p domain.PolicyError
		switch {
		case errors.Is(err, domain.ErrNotFound):
			status = 404
		case errors.Is(err, domain.ErrConflict):
			status = 409
		case errors.As(err, &p):
			status = 422
		default:
			status = 500
			err = errors.New("Unable to complete the operation.")
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		jsonResponse(w, status, map[string]string{"detail": err.Error()})
	} else {
		a.render(w, r, "error", map[string]any{"Message": err.Error()}, status)
	}
}
func (a *App) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any, status int) {
	cookie, _ := r.Cookie("gt_csrf")
	token := ""
	if cookie != nil {
		token = cookie.Value
	}
	if len(token) != 32 {
		for _, header := range w.Header().Values("Set-Cookie") {
			response := http.Response{Header: http.Header{"Set-Cookie": []string{header}}}
			for _, c := range response.Cookies() {
				if c.Name == "gt_csrf" {
					token = c.Value
				}
			}
		}
	}
	data["CSRF"] = token
	data["Path"] = r.URL.Path
	data["Countries"] = policy.Countries
	data["Desktop"] = a.Options.DesktopInstance != "" && a.Options.Shutdown != nil
	data["Version"] = a.Options.Version
	data["SigningStatus"] = a.Options.SigningStatus
	titles := map[string]string{"dashboard": "Overview", "runs": "Run history", "run": "Country review", "profiles": "Profiles", "audit": "Audit trail", "settings": "Workspace settings", "docs": "API reference", "closed": "App closed", "error": "Something needs attention"}
	data["PageTitle"] = titles[name]
	var output bytes.Buffer
	if err := a.Templates.ExecuteTemplate(&output, name, data); err != nil {
		http.Error(w, "Unable to render page.", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = output.WriteTo(w)
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	err := a.Store.DB.PingContext(r.Context())
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, map[string]any{"status": "ok", "mode": "synthetic", "database": "ok", "core": "go", "embedded_worker": a.EmbeddedWorker})
}
func (a *App) gaps(w http.ResponseWriter, r *http.Request) {
	var c domain.Counts
	err := a.Store.Transaction(func(repo *storage.Repository) error { var e error; c, e = repo.Counts(); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, map[string]any{"field": "country", "total": c.Total, "missing": c.Missing, "existing": c.Existing, "mode": "synthetic"})
}
func (a *App) fields(w http.ResponseWriter, r *http.Request) {
	catalog, err := domain.Catalog()
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, catalog)
}
func queryInt(r *http.Request, key string, fallback, low, high int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < low || n > high {
		return 0, domain.Invalid("Invalid pagination parameter.")
	}
	return n, nil
}
func (a *App) candidates(w http.ResponseWriter, r *http.Request) {
	after, err := queryInt(r, "after", 0, 0, math.MaxInt)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	var p domain.CandidatePage
	err = a.Store.Transaction(func(repo *storage.Repository) error {
		counts, e := repo.Counts()
		if e != nil {
			return e
		}
		p, e = repo.Page(after, counts.UpperBound, limit)
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, p)
}
func (a *App) runs(w http.ResponseWriter, r *http.Request) {
	var runs []domain.Run
	err := a.Store.Transaction(func(repo *storage.Repository) error { var e error; runs, e = repo.Runs(); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, runs)
}
func formKeys(r *http.Request, allowed ...string) error {
	for key := range r.PostForm {
		ok := key == "_csrf"
		for _, v := range allowed {
			ok = ok || key == v
		}
		if !ok {
			return domain.Invalid("Unsupported form field.")
		}
	}
	return nil
}
func api(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }
func (a *App) newRun(w http.ResponseWriter, r *http.Request) {
	request := domain.RunRequest{Mode: "preview", Fields: []string{"country"}}
	var err error
	if api(r) {
		err = decode(r, &request)
	} else {
		err = formKeys(r, "mode", "fields")
		request.Mode = r.PostForm.Get("mode")
		request.Fields = r.PostForm["fields"]
	}
	if err == nil {
		err = request.Validate()
	}
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	var run domain.Run
	err = a.Store.Transaction(func(repo *storage.Repository) error { var e error; run, e = repo.NewRun(request); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if api(r) {
		jsonResponse(w, 202, run)
	} else {
		http.Redirect(w, r, "/runs/"+run.ID, 303)
	}
}
func (a *App) run(w http.ResponseWriter, r *http.Request) {
	var run domain.Run
	err := a.Store.Transaction(func(repo *storage.Repository) error { var e error; run, e = repo.Run(r.PathValue("id")); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, run)
}
func (a *App) changeRun(w http.ResponseWriter, r *http.Request) {
	var run domain.Run
	err := a.Store.Transaction(func(repo *storage.Repository) error {
		var e error
		run, e = repo.ChangeRun(r.PathValue("id"), r.PathValue("action"))
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if api(r) {
		jsonResponse(w, 200, run)
	} else {
		http.Redirect(w, r, "/runs/"+run.ID, 303)
	}
}
func (a *App) suggestions(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	var items []domain.Suggestion
	err = a.Store.Transaction(func(repo *storage.Repository) error {
		if _, e := repo.Run(r.PathValue("id")); e != nil {
			return e
		}
		var e error
		items, e = repo.Suggestions(r.PathValue("id"), r.URL.Query().Get("after"), limit+1)
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	var next *string
	if len(items) > limit {
		next = &items[limit-1].ID
		items = items[:limit]
	}
	jsonResponse(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func (a *App) writes(w http.ResponseWriter, r *http.Request) {
	var items []domain.Writeback
	err := a.Store.Transaction(func(repo *storage.Repository) error {
		if _, e := repo.Run(r.PathValue("id")); e != nil {
			return e
		}
		var e error
		items, e = repo.Writes(r.PathValue("id"))
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, items)
}
func (a *App) approve(w http.ResponseWriter, r *http.Request) {
	var approval domain.Approval
	var err error
	if api(r) {
		err = decode(r, &approval)
	} else {
		err = formKeys(r, "value", "reason")
		value := r.PostForm.Get("value")
		if value != "" {
			approval.Value = &value
		}
		approval.Reason = r.PostForm.Get("reason")
	}
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	result, err := a.Review.Approve(r.PathValue("id"), approval)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if api(r) {
		jsonResponse(w, 200, result)
	} else {
		a.reviewRedirect(w, r)
	}
}
func (a *App) reject(w http.ResponseWriter, r *http.Request) {
	if err := a.Review.Reject(r.PathValue("id")); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if api(r) {
		jsonResponse(w, 200, map[string]string{"state": "rejected"})
	} else {
		a.reviewRedirect(w, r)
	}
}
func (a *App) reviewRedirect(w http.ResponseWriter, r *http.Request) {
	var s domain.Suggestion
	err := a.Store.Transaction(func(repo *storage.Repository) error { var e error; s, e = repo.Suggestion(r.PathValue("id")); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	http.Redirect(w, r, "/runs/"+s.RunID, 303)
}
func (a *App) undo(w http.ResponseWriter, r *http.Request) {
	state, err := a.Review.Undo(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if api(r) {
		jsonResponse(w, 200, map[string]string{"state": state})
		return
	}
	var write domain.Writeback
	err = a.Store.Transaction(func(repo *storage.Repository) error { var e error; write, e = repo.Write(r.PathValue("id")); return e })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	http.Redirect(w, r, "/runs/"+write.RunID, 303)
}
func (a *App) edit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		a.fail(w, r, domain.ErrNotFound, 0)
		return
	}
	var edit struct {
		Country *string `json:"country"`
	}
	if err = decode(r, &edit); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if !domain.Empty(edit.Country) {
		code := policy.Normalize(*edit.Country)
		if code == "" {
			a.fail(w, r, domain.Invalid("Choose a valid country or clear the field."), 0)
			return
		}
		edit.Country = &code
	}
	var candidate domain.Candidate
	err = a.Store.Transaction(func(repo *storage.Repository) error {
		var e error
		candidate, e = repo.EditCountry(id, edit.Country)
		if e != nil {
			return e
		}
		return repo.Audit("demo_country_edited", nil, &id, map[string]any{"country": edit.Country})
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	jsonResponse(w, 200, candidate)
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	err := a.Store.Transaction(func(repo *storage.Repository) error {
		counts, e := repo.Counts()
		if e != nil {
			return e
		}
		runs, e := repo.Runs()
		data["Counts"], data["Runs"] = counts, runs
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	a.render(w, r, "dashboard", data, 200)
}
func (a *App) runsPage(w http.ResponseWriter, r *http.Request) {
	var runs []domain.Run
	err := a.Store.Transaction(func(repo *storage.Repository) error { var err error; runs, err = repo.Runs(); return err })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	a.render(w, r, "runs", map[string]any{"Runs": runs}, 200)
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	var counts domain.Counts
	err := a.Store.Transaction(func(repo *storage.Repository) error { var err error; counts, err = repo.Counts(); return err })
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	directory := ""
	if a.Options.DatabasePath != "" {
		path, err := filepath.Abs(a.Options.DatabasePath)
		if err != nil {
			a.fail(w, r, err, 0)
			return
		}
		directory = filepath.Dir(path)
	}
	a.render(w, r, "settings", map[string]any{"Counts": counts, "DataDirectory": directory, "DocumentWorker": a.Options.DocumentWorker, "EmbeddedWorker": a.EmbeddedWorker, "Restored": r.URL.Query().Get("restored") == "1"}, 200)
}

func (a *App) runPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	err := a.Store.Transaction(func(repo *storage.Repository) error {
		run, e := repo.Run(r.PathValue("id"))
		if e != nil {
			return e
		}
		items, e := repo.Suggestions(run.ID, r.URL.Query().Get("after"), 51)
		if e != nil {
			return e
		}
		next := ""
		if len(items) > 50 {
			next = items[49].ID
			items = items[:50]
		}
		writes, e := repo.Writes(run.ID)
		if e != nil {
			return e
		}
		job, e := repo.JobStatus(run.ID)
		if e != nil {
			return e
		}
		var pending, applied int
		if e = repo.Tx.QueryRow("SELECT COUNT(*) FROM suggestions WHERE run_id=? AND state='pending'", run.ID).Scan(&pending); e != nil {
			return e
		}
		if e = repo.Tx.QueryRow("SELECT COUNT(*) FROM writebacks WHERE run_id=? AND state='applied'", run.ID).Scan(&applied); e != nil {
			return e
		}
		data["Job"], data["Pending"], data["Applied"] = job, pending, applied
		data["Run"], data["Suggestions"], data["NextCursor"], data["Writes"] = run, items, next, writes
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	a.render(w, r, "run", data, 200)
}
func (a *App) profiles(w http.ResponseWriter, r *http.Request) {
	after, err := queryInt(r, "after", 0, 0, math.MaxInt)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	var page domain.CandidatePage
	err = a.Store.Transaction(func(repo *storage.Repository) error {
		counts, e := repo.Counts()
		if e != nil {
			return e
		}
		page, e = repo.Page(after, counts.UpperBound, 100)
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	a.render(w, r, "profiles", map[string]any{"Page": page}, 200)
}
func (a *App) auditPage(w http.ResponseWriter, r *http.Request) {
	before := math.MaxFloat64
	query := r.URL.Query()
	beforeID := query.Get("before_id")
	if len(query["before"]) > 1 || len(query["before_id"]) > 1 || (query.Has("before_id") && (!query.Has("before") || len(beforeID) != 32)) {
		a.fail(w, r, domain.Invalid("Invalid audit cursor."), 0)
		return
	}
	if beforeID != "" {
		if _, err := hex.DecodeString(beforeID); err != nil || beforeID != strings.ToLower(beforeID) {
			a.fail(w, r, domain.Invalid("Invalid audit cursor."), 0)
			return
		}
	}
	if raw := query.Get("before"); query.Has("before") {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			a.fail(w, r, domain.Invalid("Invalid audit cursor."), 0)
			return
		}
		before = value
	}
	var events []domain.AuditEvent
	err := a.Store.Transaction(func(repo *storage.Repository) error {
		var e error
		events, e = repo.Audits(before, beforeID, 51)
		return e
	})
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	next, nextID := "", ""
	if len(events) > 50 {
		next = strconv.FormatFloat(events[49].CreatedAt, 'g', -1, 64)
		nextID = events[49].ID
		events = events[:50]
	}
	a.render(w, r, "audit", map[string]any{"Events": events, "NextBefore": next, "NextBeforeID": nextID}, 200)
}
