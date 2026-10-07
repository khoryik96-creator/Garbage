// Package desktop hosts the same Go application with a per-user workspace,
// one running instance and automatic browser opening. It requires no Python runtime.
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/application"
	"github.com/khoryik96-creator/Garbage/internal/config"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

var errInUse = errors.New("workspace is already open")

type Options struct {
	DataDirectory string
	OpenBrowser   func(string) error
}
type instance struct {
	URL string `json:"url"`
	ID  string `json:"instance_id"`
}

func DataDirectory() (string, error) {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("LOCALAPPDATA"); root != "" {
			return filepath.Join(root, "GarbageTruck"), nil
		}
	}
	if runtime.GOOS == "linux" {
		root := os.Getenv("XDG_DATA_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			root = filepath.Join(home, ".local", "share")
		}
		if !filepath.IsAbs(root) {
			return "", errors.New("XDG_DATA_HOME must be an absolute path")
		}
		return filepath.Join(root, "GarbageTruck"), nil
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "GarbageTruck"), nil
}

func readInstance(path string) (instance, error) {
	var info instance
	data, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	u, err := url.Parse(info.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || len(info.ID) != 32 {
		return info, errors.New("invalid local instance")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return info, errors.New("invalid local port")
	}
	return info, nil
}

func ready(ctx context.Context, info instance) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL+"api/desktop/instance", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 300 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var result struct {
		ID string `json:"instance_id"`
	}
	return response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) == nil && result.ID == info.ID
}

// acquireOrReopen retries the lock while an old instance drains requests.
// A successful reopen returns a nil unlock; a new owner returns its lock.
func acquireOrReopen(ctx context.Context, directory string, open func(string) error) (func(), error) {
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	manualURL := ""
	for {
		unlock, err := acquire(filepath.Join(directory, "app.lock"))
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, errInUse) {
			return nil, errors.New("The workspace folder cannot be opened.")
		}
		info, err := readInstance(filepath.Join(directory, "instance.json"))
		if err == nil && ready(deadline, info) {
			if open != nil {
				req, _ := http.NewRequestWithContext(deadline, http.MethodPost, info.URL+"api/desktop/reopen", nil)
				req.Header.Set("X-Garbage-Instance", info.ID)
				client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				response, requestErr := client.Do(req)
				if requestErr != nil {
					// The owner may exit between readiness and IPC. Retry the lock
					// instead of abandoning a requested replacement launch.
					manualURL = info.URL
				} else {
					response.Body.Close()
					if response.StatusCode == http.StatusNoContent {
						return nil, nil
					}
					if response.StatusCode != http.StatusServiceUnavailable {
						return nil, errors.New("The browser could not be opened. Open " + info.URL + " manually. See app.log for details.")
					}
				}
			} else {
				return nil, nil
			}
		}
		select {
		case <-deadline.Done():
			if manualURL != "" {
				return nil, errors.New("The interface could not be reopened. If the app is still running, open " + manualURL + " manually. See app.log for details.")
			}
			return nil, errors.New("Garbage Truck is still starting or saving its work. Try opening it again in a moment. See app.log in your workspace folder for details.")
		case <-ticker.C:
		}
	}
}

func Run(parent context.Context, options Options) error {
	directory, err := filepath.Abs(options.DataDirectory)
	if err != nil || options.DataDirectory == "" {
		return errors.New("Choose a valid workspace folder.")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return errors.New("The workspace folder cannot be created.")
	}
	unlock, err := acquireOrReopen(parent, directory, options.OpenBrowser)
	path := filepath.Join(directory, "instance.json")
	if err != nil {
		return err
	}
	if unlock == nil {
		return nil
	}
	defer unlock()
	settings, err := config.Load()
	if err != nil {
		return err
	}
	settings.DatabasePath = filepath.Join(directory, "autocoder.db")
	settings.EmbeddedWorker = true
	settings.RecoverExclusive = true
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	info := instance{ID: domain.ID()}
	open := options.OpenBrowser
	if open == nil {
		open = OpenBrowser
	}
	app, err := application.Open(settings, web.Options{DesktopInstance: info.ID, DesktopReady: func() bool { return ctx.Err() == nil }, ReopenBrowser: func() error {
		if err := open(info.URL); err != nil {
			browserFailure(info.URL)
			return err
		}
		return nil
	}, Shutdown: cancel, SigningStatus: signingStatus()})
	if err != nil {
		return errors.New("Garbage Truck could not open its workspace. Your existing data has been kept.")
	}
	defer func() { _ = app.Close(); _ = os.Remove(path) }()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("A local connection could not be opened.")
	}
	defer listener.Close()
	info.URL = fmt.Sprintf("http://%s/", listener.Addr())
	finished := make(chan struct{})
	var serveError error
	go func() { serveError = app.Serve(ctx, listener); close(finished) }()
	defer func() { cancel(); <-finished }()
	data, _ := json.Marshal(info)
	if err = os.WriteFile(path, data, 0600); err != nil {
		return errors.New("Workspace startup information could not be saved.")
	}
	if !ready(ctx, info) {
		return errors.New("Garbage Truck did not finish starting. Try opening it again.")
	}
	log.Printf("Garbage Truck ready at %s", info.URL)
	if options.OpenBrowser != nil {
		if err = options.OpenBrowser(info.URL); err != nil {
			browserFailure(info.URL)
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case <-finished:
		return serveError
	}
}
