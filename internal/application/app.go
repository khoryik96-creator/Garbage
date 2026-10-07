// Package application wires the core once for the command-line and desktop hosts.
package application

import (
	"context"
	"errors"
	"github.com/khoryik96-creator/Garbage/internal/connectors/jobadder"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/config"
	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/docworker"
	"github.com/khoryik96-creator/Garbage/internal/jobs"
	"github.com/khoryik96-creator/Garbage/internal/storage"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

var Version = "0.5.1"

type App struct {
	Store          *storage.Store
	Handler        http.Handler
	Worker         *jobs.Worker
	EmbeddedWorker bool
	Connection     *jobadder.Connection
}

func Open(settings config.Settings, options web.Options) (*App, error) {
	store, err := storage.Open(settings.DatabasePath)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*App, error) { store.Close(); return nil, err }
	if err = store.Transaction(connectors.Seed); err != nil {
		return closeOnError(err)
	}
	if settings.RecoverExclusive {
		if err = store.Transaction(func(r *storage.Repository) error { return r.RecoverExclusive(storage.Now(), 5) }); err != nil {
			return closeOnError(err)
		}
	}
	directory, err := filepath.Abs(filepath.Dir(settings.DatabasePath))
	if err != nil {
		return closeOnError(err)
	}
	connection := jobadder.NewConnection(jobadder.NewVault(directory))
	options.JobAdder = connection
	options.Version = Version
	options.DatabasePath = settings.DatabasePath
	options.DocumentWorker = settings.DocumentWorkerURL != ""
	handler, err := web.NewWithOptions(store, settings.EmbeddedWorker, options)
	if err != nil {
		connection.Close()
		return closeOnError(err)
	}
	worker := jobs.New(store, settings.PageSize)
	if settings.DocumentWorkerURL != "" {
		client, err := docworker.New(settings.DocumentWorkerURL)
		if err != nil {
			connection.Close()
			return closeOnError(err)
		}
		worker.Extractor = client
	}
	return &App{Store: store, Handler: handler, Worker: worker, EmbeddedWorker: settings.EmbeddedWorker, Connection: connection}, nil
}

func (a *App) Close() error {
	if a.Connection != nil {
		a.Connection.Close()
	}
	return a.Store.Close()
}

// Serve owns worker and HTTP shutdown. Callers close the database after it returns.
func (a *App) Serve(parent context.Context, listener net.Listener) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	server := &http.Server{Handler: a.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	var group sync.WaitGroup
	if a.EmbeddedWorker {
		group.Go(func() { a.Worker.Run(ctx) })
	}
	group.Go(func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	})
	err := server.Serve(listener)
	cancel()
	group.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
