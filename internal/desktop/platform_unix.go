//go:build linux || darwin

package desktop

import (
	"log"
	"os/exec"
	"runtime"
)

func OpenBrowser(url string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	process := exec.Command(command, url)
	if err := process.Start(); err != nil {
		return err
	}
	go func() { _ = process.Wait() }()
	return nil
}
func ShowError(message string) { log.Print(message) }
