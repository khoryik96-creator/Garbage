package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

type browser struct {
	handler http.Handler
	token   string
}

func (b browser) request(method, path, body string, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if b.token != "" {
		request.AddCookie(&http.Cookie{Name: "gt_csrf", Value: b.token})
	}
	if csrf {
		request.Header.Set("X-CSRF-Token", b.token)
	}
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	return response
}
func startBrowser(t *testing.T) browser {
	t.Helper()
	s, _, _ := setup(t)
	handler, err := web.New(s, false)
	must(t, err)
	b := browser{handler: handler}
	response := b.request("GET", "/", "", false)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "gt_csrf" {
			b.token = cookie.Value
		}
	}
	if b.token == "" || !strings.Contains(response.Body.String(), b.token) {
		t.Fatal("CSRF token missing from form")
	}
	return b
}
func TestPagesCatalogueAndPagination(t *testing.T) {
	b := startBrowser(t)
	for _, path := range []string{"/", "/runs", "/settings", "/profiles", "/audit", "/static/app.css", "/static/app.js", "/static/icon.svg", "/api/docs", "/api/openapi.json", "/api/health", "/api/fields"} {
		t.Run(path, func(t *testing.T) {
			r := b.request("GET", path, "", false)
			if r.Code != 200 {
				t.Fatalf("failed page: %d %s", r.Code, r.Body.String())
			}
		})
	}
	var catalogue domain.FieldCatalog
	must(t, json.Unmarshal(b.request("GET", "/api/fields", "", false).Body.Bytes(), &catalogue))
	if len(catalogue.Fields) != 20 {
		t.Fatal("lost field mappings")
	}
	for _, f := range catalogue.Fields {
		if f.PublicMappingVerified {
			t.Fatal("unverified live mapping enabled")
		}
	}
	for _, path := range []string{"/api/candidates?limit=10000", "/api/candidates?after=-1", "/api/candidates?limit=0", "/audit?before=NaN"} {
		if r := b.request("GET", path, "", false); r.Code != 422 {
			t.Fatal("bad pagination accepted")
		}
	}
}

func TestDeveloperHostDoesNotOfferDesktopShutdown(t *testing.T) {
	b := startBrowser(t)
	settings := b.request("GET", "/settings", "", false)
	if !strings.Contains(settings.Body.String(), "Your demo workspace") || !strings.Contains(settings.Body.String(), "Not connected") || strings.Contains(settings.Body.String(), "/desktop/quit") {
		t.Fatal("settings misrepresented connection or host")
	}
	if b.request("POST", "/desktop/quit", "", true).Code != 404 {
		t.Fatal("developer web host exposed desktop shutdown")
	}
}

func TestQuitImmediatelyMarksInstanceAsSaving(t *testing.T) {
	s, _, _ := setup(t)
	draining := make(chan struct{})
	defer close(draining)
	handler, err := web.NewWithOptions(s, true, web.Options{DesktopInstance: domain.ID(), Shutdown: func() { <-draining }})
	must(t, err)
	b := browser{handler: handler, token: domain.ID()}
	if b.request("GET", "/api/desktop/instance", "", false).Code != 200 {
		t.Fatal("instance not ready")
	}
	if b.request("POST", "/desktop/quit", "", true).Code != 200 {
		t.Fatal("quit failed")
	}
	if b.request("GET", "/api/desktop/instance", "", false).Code != 503 {
		t.Fatal("relaunch could reopen an instance after quit was requested")
	}
}
func TestStrictRequestsAndCSRF(t *testing.T) {
	b := startBrowser(t)
	for _, body := range []string{`{"fields":["unsupported"]}`, `{"mode":"auto_fill"}`, `{"fields":[]}`, `{"fields":["country","country"]}`, `{"overwrite":true}`, `{"mode":null}`, `{"fields":null}`, `{"mode":"preview","mode":"review"}`, `null`, `{} {}`, `[]`} {
		t.Run(body, func(t *testing.T) {
			if r := b.request("POST", "/api/runs", body, true); r.Code != 422 {
				t.Fatalf("invalid body accepted: %d", r.Code)
			}
		})
	}
	if b.request("POST", "/api/runs", `{}`, false).Code != 403 {
		t.Fatal("missing csrf accepted")
	}
	request := httptest.NewRequest("POST", "http://localhost/api/runs", strings.NewReader(`{}`))
	request.AddCookie(&http.Cookie{Name: "gt_csrf", Value: b.token})
	request.Header.Set("X-CSRF-Token", b.token)
	request.Header.Set("Origin", "https://other.test")
	response := httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("cross origin accepted")
	}
	request = httptest.NewRequest("GET", "http://evil.test/", nil)
	response = httptest.NewRecorder()
	b.handler.ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("unrecognized host accepted")
	}
}
func TestHTTPReviewApproveUndoAndForms(t *testing.T) {
	s, w, _ := setup(t)
	handler, err := web.New(s, false)
	must(t, err)
	b := browser{handler: handler, token: domain.ID()}
	created := b.request("POST", "/api/runs", `{"mode":"review","fields":["country"]}`, true)
	if created.Code != 202 {
		t.Fatal(created.Body.String())
	}
	var run domain.Run
	must(t, json.Unmarshal(created.Body.Bytes(), &run))
	drain(t, w)
	page := b.request("GET", "/runs/"+run.ID, "", false)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Approve Country") {
		t.Fatal("review form missing")
	}
	suggestion := proposal(t, s, run.ID, 1001)
	approved := b.request("POST", "/api/suggestions/"+suggestion.ID+"/approve", `{}`, true)
	if approved.Code != 200 {
		t.Fatal(approved.Body.String())
	}
	var result domain.ReviewResult
	must(t, json.Unmarshal(approved.Body.Bytes(), &result))
	undone := b.request("POST", "/api/writebacks/"+*result.WritebackID+"/undo", "", true)
	if undone.Code != 200 || !strings.Contains(undone.Body.String(), "undone") {
		t.Fatal("undo failed")
	}
	if !strings.Contains(b.request("GET", "/audit", "", false).Body.String(), "Country Approved") {
		t.Fatal("audit missing")
	}
	form := url.Values{"_csrf": {b.token}, "mode": {"preview"}, "fields": {"country"}}
	request := httptest.NewRequest("POST", "http://localhost/runs", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "gt_csrf", Value: b.token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 303 {
		t.Fatal(response.Body.String())
	}
	seen := map[string]bool{}
	after := ""
	for {
		response := b.request("GET", fmt.Sprintf("/api/runs/%s/suggestions?limit=2&after=%s", run.ID, after), "", false)
		var page struct {
			Items []domain.Suggestion `json:"items"`
			Next  *string             `json:"next_cursor"`
		}
		must(t, json.Unmarshal(response.Body.Bytes(), &page))
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate pagination item")
			}
			seen[item.ID] = true
		}
		if page.Next == nil {
			break
		}
		after = *page.Next
	}
	if len(seen) != 6 {
		t.Fatal("pagination lost proposals")
	}
}
