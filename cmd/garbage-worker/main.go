package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/khoryik96-creator/Garbage/internal/config"
	"github.com/khoryik96-creator/Garbage/internal/connectors"
	"github.com/khoryik96-creator/Garbage/internal/docworker"
	"github.com/khoryik96-creator/Garbage/internal/jobs"
	"github.com/khoryik96-creator/Garbage/internal/storage"
)

func main() {
	s, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	store, err := storage.Open(s.DatabasePath)
	if err != nil {
		log.Fatal("Database initialization failed.")
	}
	defer store.Close()
	if err = store.Transaction(connectors.Seed); err != nil {
		log.Fatal("Demo initialization failed.")
	}
	worker := jobs.New(store, s.PageSize)
	if s.DocumentWorkerURL != "" {
		client, err := docworker.New(s.DocumentWorkerURL)
		if err != nil {
			log.Fatal(err)
		}
		worker.Extractor = client
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Print("Go job worker started.")
	worker.Run(ctx)
}
