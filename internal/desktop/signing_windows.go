package desktop

import (
	"os"
	"syscall"
	"unsafe"
)

type trustFile struct {
	Size         uint32
	Path         *uint16
	Handle       uintptr
	KnownSubject *syscall.GUID
}
type trustData struct {
	Size              uint32
	PolicyCallback    uintptr
	SIPClient         uintptr
	UIChoice          uint32
	RevocationChecks  uint32
	UnionChoice       uint32
	File              *trustFile
	StateAction       uint32
	StateData         uintptr
	URLReference      *uint16
	ProviderFlags     uint32
	UIContext         uint32
	SignatureSettings uintptr
}

func signingStatus() string {
	path, err := os.Executable()
	if err != nil {
		return "Signature status unavailable"
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "Signature status unavailable"
	}
	file := trustFile{Path: name}
	file.Size = uint32(unsafe.Sizeof(file))
	// WINTRUST_ACTION_GENERIC_VERIFY_V2. Cache-only retrieval keeps startup
	// independent of network certificate checks; Windows still verifies trust.
	action := syscall.GUID{Data1: 0x00aac56b, Data2: 0xcd44, Data3: 0x11d0, Data4: [8]byte{0x8c, 0xc2, 0, 0xc0, 0x4f, 0xc2, 0x95, 0xee}}
	data := trustData{UIChoice: 2, UnionChoice: 1, File: &file, ProviderFlags: 0x1000}
	data.Size = uint32(unsafe.Sizeof(data))
	status, _, _ := syscall.NewLazyDLL("wintrust.dll").NewProc("WinVerifyTrust").Call(0, uintptr(unsafe.Pointer(&action)), uintptr(unsafe.Pointer(&data)))
	switch uint32(status) {
	case 0:
		return "Signature verified by Windows"
	case 0x800b0100:
		return "Unsigned"
	default:
		return "Signature could not be verified by Windows"
	}
}
