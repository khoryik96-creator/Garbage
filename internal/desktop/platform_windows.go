package desktop

import (
	"fmt"
	"syscall"
	"unsafe"
)

type shellExecuteInfo struct {
	Size, Mask                        uint32
	Window                            uintptr
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance                          uintptr
	IDList                            uintptr
	Class                             *uint16
	ClassKey                          uintptr
	HotKey                            uint32
	Icon                              uintptr
	Process                           syscall.Handle
}

func OpenBrowser(url string) error { return launchShell(url, "", browserFailure) }

func launchShell(url, parameters string, report func(string)) error {
	// ShellExecuteEx reports association errors without an OS error dialog, and
	// returns a process handle when the handler supplies one. Browser delegation
	// can legitimately return no handle, so it cannot always be exit-observed.
	verb, _ := syscall.UTF16PtrFromString("open")
	address, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	args, err := syscall.UTF16PtrFromString(parameters)
	if err != nil {
		return err
	}
	info := shellExecuteInfo{Mask: 0x440, Verb: verb, File: address, Parameters: args, Show: 1}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return fmt.Errorf("Windows could not open the default browser: %w", callErr)
	}
	if info.Process != 0 {
		go func() {
			defer syscall.CloseHandle(info.Process)
			status, err := syscall.WaitForSingleObject(info.Process, syscall.INFINITE)
			var code uint32
			if err != nil || status != syscall.WAIT_OBJECT_0 || syscall.GetExitCodeProcess(info.Process, &code) != nil || code != 0 {
				report(url)
			}
		}()
	}
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
