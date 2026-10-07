package desktop

import (
	"log"
	"os/exec"
)

func browserFailure(url string) {
	message := "Your browser could not be opened automatically. Open " + url + " to use Garbage Truck. You can also open the Garbage Truck shortcut again."
	log.Print(message)
	// MessageBox on Windows must not hold up application shutdown.
	go ShowError(message)
}

// Openers can remain alive for the lifetime of a browser. Reap them in the
// background, but retain their exit status and the usable manual URL.
func startBrowser(process *exec.Cmd, url string, report func(string)) error {
	if err := process.Start(); err != nil {
		return err
	}
	go func() {
		if err := process.Wait(); err != nil {
			report(url)
		}
	}()
	return nil
}
