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

func reopen(ctx context.Context, path string, open func(string) error) error {
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := readInstance(path)
		if err == nil && ready(deadline, info) {
			if open != nil {
				return open(info.URL)
			}
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("Garbage Truck is already starting. Try opening it again in a moment.")
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
	unlock, err := acquire(filepath.Join(directory, "app.lock"))
	path := filepath.Join(directory, "instance.json")
	if errors.Is(err, errInUse) {
		return reopen(parent, path, options.OpenBrowser)
	}
	if err != nil {
		return errors.New("The workspace folder cannot be opened.")
	}
	defer unlock()
	settings, err := config.Load()
	if err != nil {
		return err
	}
	settings.DatabasePath = filepath.Join(directory, "autocoder.db")
	settings.EmbeddedWorker = true
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	info := instance{ID: domain.ID()}
	app, err := application.Open(settings, web.Options{DesktopInstance: info.ID, Shutdown: cancel})
	if err != nil {
		return errors.New("Garbage Truck could not open its workspace. Your existing data has been kept.")
	}
	defer app.Close()
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
	defer os.Remove(path)
	if !ready(ctx, info) {
		return errors.New("Garbage Truck did not finish starting. Try opening it again.")
	}
	log.Printf("Garbage Truck ready at %s", info.URL)
	if options.OpenBrowser != nil {
		if err = options.OpenBrowser(info.URL); err != nil {
			ShowError("Your browser could not be opened automatically. Open " + info.URL + " to use Garbage Truck.")
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case <-finished:
		return serveError
	}
}
