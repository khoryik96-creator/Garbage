package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/khoryik96-creator/Garbage/internal/application"
	"github.com/khoryik96-creator/Garbage/internal/config"
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
	app, err := application.Open(settings, web.Options{})
	if err != nil {
		log.Fatal("Application initialization failed. Check database and worker settings.")
	}
	defer app.Close()
	if *initOnly {
		fmt.Println("Database initialized; existing records retained.")
		return
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal("Local port unavailable. Choose another port with --port.")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("Garbage Truck Go core listening on %s (pid %d)", listener.Addr(), os.Getpid())
	if err = app.Serve(ctx, listener); err != nil {
		log.Fatal("Server stopped unexpectedly.")
	}
}
