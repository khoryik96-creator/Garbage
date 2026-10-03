package desktop

import (
	"os/exec"
	"syscall"
	"unsafe"
)

func OpenBrowser(url string) error {
	process := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	if err := process.Start(); err != nil {
		return err
	}
	go func() { _ = process.Wait() }()
	return nil
}
func ShowError(message string) {
	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return
	}
	title, _ := syscall.UTF16PtrFromString("Garbage Truck")
	box := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = box.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}
