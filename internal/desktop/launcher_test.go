package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

func TestRelaunchWhileRequestDrainsKeepsHistory(t *testing.T) {
	t.Setenv("AUTOCODER_DOCUMENT_WORKER_URL", "")
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstCtx, stopFirst := context.WithCancel(ctx)
	defer stopFirst()
	opened := make(chan string, 2)
	open := func(url string) error { opened <- url; return nil }
	first := make(chan error, 1)
	go func() { first <- Run(firstCtx, Options{DataDirectory: directory, OpenBrowser: open}) }()
	var base string
	select {
	case base = <-opened:
	case <-ctx.Done():
		t.Fatal("startup timed out")
	}
	response, err := http.Get(base + "settings")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	csrf := response.Cookies()[0].Value
	u, _ := url.Parse(base)
	connection, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(8 * time.Second))
	body := `{"mode":"review","fields":["country"]}`
	_, err = fmt.Fprintf(connection, "POST /api/runs HTTP/1.1\r\nHost: %s\r\nCookie: gt_csrf=%s\r\nX-CSRF-Token: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", u.Host, csrf, csrf, len(body), body[:1])
	if err != nil {
		t.Fatal(err)
	}
	// Leave the authenticated request body pending while shutdown drains it.
	time.Sleep(100 * time.Millisecond)
	stopFirst()
	second := make(chan error, 1)
	go func() { second <- Run(ctx, Options{DataDirectory: directory, OpenBrowser: open}) }()
	select {
	case <-first:
		t.Fatal("shutdown did not wait for the pending HTTP request")
	case <-opened:
		t.Fatal("relaunch reopened an instance that is saving")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = os.Stat(filepath.Join(directory, "instance.json")); err != nil {
		t.Fatal("metadata removed before shutdown completed", err)
	}
	_, err = io.WriteString(connection, body[1:])
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	var saved domain.Run
	err = json.NewDecoder(response.Body).Decode(&saved)
	response.Body.Close()
	if err != nil || response.StatusCode != 202 {
		t.Fatal("pending request was lost", err)
	}
	select {
	case err = <-first:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first instance did not stop")
	}
	select {
	case base = <-opened:
	case <-ctx.Done():
		t.Fatal("relaunch did not acquire the released lock")
	}
	response, err = http.Get(base + "api/runs/" + saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	var history domain.Run
	err = json.NewDecoder(response.Body).Decode(&history)
	response.Body.Close()
	if err != nil || history.ID != saved.ID {
		t.Fatal("relaunch lost saved history", err)
	}
	cancel()
	select {
	case err = <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement did not stop")
	}
}

func TestBrowserHelperProcess(t *testing.T) {
	if os.Getenv("GT_BROWSER_HELPER") == "" {
		return
	}
	if os.Getenv("GT_BROWSER_HELPER") == "delayed" {
		time.Sleep(500 * time.Millisecond)
		os.Exit(7)
	}
	if os.Getenv("GT_BROWSER_HELPER") == "fail" {
		os.Exit(7)
	}
	if os.Getenv("GT_BROWSER_HELPER") == "wait" {
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestBrowserExitFailuresAreObservedWithoutBlocking(t *testing.T) {
	for _, mode := range []string{"fail", "wait"} {
		t.Run(mode, func(t *testing.T) {
			process := exec.Command(os.Args[0], "-test.run=TestBrowserHelperProcess")
			process.Env = append(os.Environ(), "GT_BROWSER_HELPER="+mode)
			reported := make(chan string, 1)
			start := time.Now()
			err := startBrowser(process, "http://127.0.0.1:8000/", func(url string) { reported <- url })
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("opener blocked startup")
			}
			if mode == "wait" {
				_ = process.Process.Kill()
			}
			select {
			case url := <-reported:
				if url != "http://127.0.0.1:8000/" {
					t.Fatal("manual URL lost")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("asynchronous browser failure hidden")
			}
		})
	}
}

func TestLaunchReopenQuitAndRestartKeepsWorkspace(t *testing.T) {
	t.Setenv("AUTOCODER_DOCUMENT_WORKER_URL", "")
	directory := filepath.Join(t.TempDir(), "workspace with spaces")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	opened := make(chan string, 4)
	launch := func() chan error {
		finished := make(chan error, 1)
		go func() {
			finished <- Run(ctx, Options{DataDirectory: directory, OpenBrowser: func(url string) error { opened <- url; return nil }})
		}()
		return finished
	}
	awaitURL := func() string {
		t.Helper()
		select {
		case value := <-opened:
			return value
		case <-ctx.Done():
			t.Fatal("launcher did not open the UI")
			return ""
		}
	}
	finished := launch()
	base := awaitURL()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Second}
	response, err := client.Get(base + "settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Save and quit app") || !strings.Contains(string(body), directory) {
		t.Fatal("installed workspace settings are missing")
	}
	u, _ := url.Parse(base)
	csrf := ""
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "gt_csrf" {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("desktop CSRF protection missing")
	}
	request, _ := http.NewRequest(http.MethodPost, base+"api/runs", strings.NewReader(`{"mode":"review","fields":["country"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var run domain.Run
	err = json.NewDecoder(response.Body).Decode(&run)
	response.Body.Close()
	if err != nil || response.StatusCode != 202 {
		t.Fatal("desktop run did not start", err)
	}
	for {
		response, err = client.Get(base + "api/runs/" + run.ID)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(response.Body).Decode(&run)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if run.State == "completed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("run did not finish")
		case <-time.After(50 * time.Millisecond):
		}
	}
	if run.Processed != 10 || run.Proposed != 6 {
		t.Fatalf("desktop changed workflow results: %+v", run)
	}
	if err = Run(ctx, Options{DataDirectory: directory, OpenBrowser: func(url string) error { opened <- url; return nil }}); err != nil {
		t.Fatal("second launch did not reopen", err)
	}
	if reused := awaitURL(); reused != base {
		t.Fatal("second launch opened a different database/server")
	}
	select {
	case <-finished:
		t.Fatal("reopening stopped the original app")
	default:
	}
	response, err = client.PostForm(base+"desktop/quit", url.Values{"_csrf": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("quit bypassed CSRF protection")
	}
	response, err = client.PostForm(base+"desktop/quit", url.Values{"_csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "Your work is saved.") || strings.Contains(string(body), "/static/") {
		t.Fatal("quit did not return a self-contained saved-work page")
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("desktop did not quit")
	}
	if _, err = os.Stat(filepath.Join(directory, "instance.json")); !os.IsNotExist(err) {
		t.Fatal("quit left a stale instance record")
	}
	store, err := storage.Open(filepath.Join(directory, "autocoder.db"))
	if err != nil {
		t.Fatal(err)
	}
	var saved domain.Run
	err = store.Transaction(func(r *storage.Repository) error { var err error; saved, err = r.Run(run.ID); return err })
	store.Close()
	if err != nil || saved.Processed != 10 {
		t.Fatal("quit lost the completed run")
	}
	finished = launch()
	base = awaitURL()
	response, err = client.Get(base + "api/runs/" + run.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewDecoder(response.Body).Decode(&saved)
	response.Body.Close()
	if err != nil || saved.ID != run.ID || saved.Proposed != 6 {
		t.Fatal("restart reset the saved workspace")
	}
	cancel()
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("desktop context cancellation did not stop")
	}
}

func TestReopenDoesNotTrustAnotherLocalService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"instance_id":"another_application"}`)
	}))
	defer server.Close()
	directory := t.TempDir()
	unlock, err := acquire(filepath.Join(directory, "app.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	data, _ := json.Marshal(instance{URL: server.URL + "/", ID: domain.ID()})
	if err = os.WriteFile(filepath.Join(directory, "instance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	opened := false
	err = Run(ctx, Options{DataDirectory: directory, OpenBrowser: func(string) error { opened = true; return nil }})
	if err == nil || opened {
		t.Fatal("launcher reused an unrelated service")
	}
}

func TestInstanceRecordRejectsExternalOrMalformedURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.json")
	for _, address := range []string{"https://127.0.0.1:8000/", "http://example.com:8000/", "http://user:pass@127.0.0.1:8000/", "http://127.0.0.1:8000/path", "http://127.0.0.1:8000/?redirect=1", "http://127.0.0.1:8000/#fragment", "http://127.0.0.1/", "http://127.0.0.1:99999/"} {
		t.Run(address, func(t *testing.T) {
			data, _ := json.Marshal(instance{URL: address, ID: domain.ID()})
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readInstance(path); err == nil {
				t.Fatal("untrusted URL accepted")
			}
		})
	}
}

func TestReopenProcessHelper(t *testing.T) {
	role := os.Getenv("GT_REOPEN_ROLE")
	if role == "" {
		return
	}
	directory := os.Getenv("GT_REOPEN_DIR")
	if role == "caller" {
		err := Run(context.Background(), Options{DataDirectory: directory, OpenBrowser: func(string) error { return errors.New("second-process opener must not be used") }})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	file, err := os.OpenFile(filepath.Join(directory, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(2)
	}
	log.SetOutput(file)
	calls := 0
	err = Run(context.Background(), Options{DataDirectory: directory, OpenBrowser: func(url string) error {
		calls++
		if calls == 1 {
			log.Print("OWNER_READY")
			return nil
		}
		process := exec.Command(os.Args[0], "-test.run=^TestBrowserHelperProcess$")
		process.Env = append(os.Environ(), "GT_BROWSER_HELPER=delayed")
		return startBrowser(process, url, browserFailure)
	}})
	if err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestReopeningProcessExitDoesNotLoseDelayedBrowserFailure(t *testing.T) {
	directory := t.TempDir()
	owner := exec.Command(os.Args[0], "-test.run=^TestReopenProcessHelper$")
	owner.Env = append(os.Environ(), "GT_REOPEN_ROLE=owner", "GT_REOPEN_DIR="+directory, "AUTOCODER_DOCUMENT_WORKER_URL=")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(directory, "app.log"))
		if strings.Contains(string(data), "OWNER_READY") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner did not start", string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	info, err := readInstance(filepath.Join(directory, "instance.json"))
	if err != nil {
		t.Fatal(err)
	}
	caller := exec.Command(os.Args[0], "-test.run=^TestReopenProcessHelper$")
	caller.Env = append(os.Environ(), "GT_REOPEN_ROLE=caller", "GT_REOPEN_DIR="+directory)
	if output, err := caller.CombinedOutput(); err != nil {
		t.Fatal("second launcher failed", err, string(output))
	}
	for {
		data, _ := os.ReadFile(filepath.Join(directory, "app.log"))
		if strings.Contains(string(data), "Your browser could not be opened automatically. Open "+info.URL) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delayed browser failure lost after second process exit", string(data))
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Invalid capabilities and browser-origin requests must not launch a helper.
	for _, origin := range []string{"", "http://evil.example"} {
		req, _ := http.NewRequest("POST", info.URL+"api/desktop/reopen", nil)
		req.Header.Set("X-Garbage-Instance", "invalid")
		if origin != "" {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-Garbage-Instance", info.ID)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("invalid IPC request accepted")
		}
	}
}

func TestRelaunchWhenOwnerExitsBetweenReadinessAndReopen(t *testing.T) {
	directory := t.TempDir()
	unlock, err := acquire(filepath.Join(directory, "app.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer release.Do(unlock)
	id := domain.ID()
	var server *httptest.Server
	server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/desktop/instance" {
			t.Errorf("unexpected request %s", r.URL.Path)
			return
		}
		w.Header().Set("Connection", "close")
		_, _ = fmt.Fprintf(w, `{"instance_id":"%s"}`, id)
		_ = server.Listener.Close()
		release.Do(unlock)
	}))
	server.Config.SetKeepAlivesEnabled(false)
	server.Start()
	defer server.Close()
	data, _ := json.Marshal(instance{URL: server.URL + "/", ID: id})
	if err := os.WriteFile(filepath.Join(directory, "instance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	newUnlock, err := acquireOrReopen(ctx, directory, func(string) error { return nil })
	if newUnlock != nil {
		defer newUnlock()
	}
	if err != nil || newUnlock == nil {
		t.Fatalf("relaunch failed after owner stopped: unlock=%v error=%v", newUnlock != nil, err)
	}
}
