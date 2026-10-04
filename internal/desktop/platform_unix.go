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
	return startBrowser(exec.Command(command, url), url, browserFailure)
}
func ShowError(message string) { log.Print(message) }
