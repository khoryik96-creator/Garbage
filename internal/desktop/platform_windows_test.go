package desktop

import (
	"os"
	"testing"
	"time"
)

func TestUnsignedExecutableIsReportedAccurately(t *testing.T) {
	// Go test executables have no Authenticode certificate. Exercise the real
	// Windows API, rather than trusting a build-time signing label.
	if status := signingStatus(); status != "Unsigned" {
		t.Fatalf("unsigned executable reported as %q", status)
	}
}

func TestWindowsShellLaunchObservesAsynchronousFailure(t *testing.T) {
	t.Setenv("GT_BROWSER_HELPER", "fail")
	reported := make(chan string, 1)
	start := time.Now()
	err := launchShell(os.Args[0], "-test.run=TestBrowserHelperProcess", func(url string) { reported <- url })
	if err != nil {
		t.Fatal("native ShellExecuteEx failed to start its handler", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("native browser handler blocked launch")
	}
	select {
	case <-reported:
	case <-time.After(5 * time.Second):
		t.Fatal("Windows hid the handler's asynchronous failure")
	}
}
