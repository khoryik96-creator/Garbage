//go:build linux || darwin

package desktop

import "runtime"

func signingStatus() string {
	if runtime.GOOS == "darwin" {
		return "Not notarized"
	}
	return "Not applicable on Linux"
}
