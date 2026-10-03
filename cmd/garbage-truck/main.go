package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/khoryik96-creator/Garbage/internal/config"
	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/docworker"
	"github.com/khoryik96-creator/Garbage/internal/jobs"
	"github.com/khoryik96-creator/Garbage/internal/storage"
	"github.com/khoryik96-creator/Garbage/internal/web"
)

func main() {
	port := flag.Int("port", 8000, "Local port")
	initOnly := flag.Bool("init-only", false, "Migrate and seed without starting")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		log.Fatal("Invalid port.")
	}
	settings, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	store, err := storage.Open(settings.DatabasePath)
	if err != nil {
		log.Fatal("Database initialization failed.")
	}
	defer store.Close()
	if err = store.Transaction(connectors.Seed); err != nil {
		log.Fatal("Demo initialization failed.")
	}
	if *initOnly {
		fmt.Println("Database initialized; existing records retained.")
		return
	}
	handler, err := web.New(store, settings.EmbeddedWorker)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	worker := jobs.New(store, settings.PageSize)
	if settings.DocumentWorkerURL != "" {
		client, err := docworker.New(settings.DocumentWorkerURL)
		if err != nil {
			log.Fatal(err)
		}
		worker.Extractor = client
	}
	var group sync.WaitGroup
	if settings.EmbeddedWorker {
		group.Go(func() { worker.Run(ctx) })
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	group.Go(func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	})
	log.Printf("Garbage Truck Go core listening on %s (pid %d)", server.Addr, os.Getpid())
	err = server.ListenAndServe()
	stop()
	group.Wait()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal("Server startup failed.")
	}
}
