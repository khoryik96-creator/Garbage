package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/khoryik96-creator/Garbage/internal/desktop"
)

func main() {
	data := flag.String("data-dir", "", "Workspace folder (default: your application data folder)")
	noBrowser := flag.Bool("no-browser", false, "Start without automatically opening the browser")
	flag.Parse()
	if *data == "" {
		directory, err := desktop.DataDirectory()
		if err != nil {
			desktop.ShowError("Your application data folder could not be found.")
			os.Exit(1)
		}
		*data = directory
	}
	if err := os.MkdirAll(*data, 0700); err != nil {
		desktop.ShowError("Your workspace folder cannot be created.")
		os.Exit(1)
	}
	file, err := os.OpenFile(filepath.Join(*data, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		desktop.ShowError("Your workspace log cannot be opened.")
		os.Exit(1)
	}
	defer file.Close()
	log.SetOutput(file)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	options := desktop.Options{DataDirectory: *data, OpenBrowser: desktop.OpenBrowser}
	if *noBrowser {
		options.OpenBrowser = nil
	}
	if err := desktop.Run(ctx, options); err != nil {
		log.Print(err)
		desktop.ShowError(err.Error())
		os.Exit(1)
	}
}
