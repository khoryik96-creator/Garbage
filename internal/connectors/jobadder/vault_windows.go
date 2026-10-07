package jobadder

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type windowsVault struct{ path string }
type dataBlob struct {
	size uint32
	data *byte
}

func NewVault(directory string) Vault {
	return windowsVault{filepath.Join(directory, "jobadder.dpapi")}
}
func (windowsVault) Available() bool { return true }
func protect(data []byte, decrypt bool) ([]byte, error) {
	input := dataBlob{size: uint32(len(data))}
	if len(data) > 0 {
		input.data = &data[0]
	}
	var output dataBlob
	proc := "CryptProtectData"
	if decrypt {
		proc = "CryptUnprotectData"
	}
	result, _, err := syscall.NewLazyDLL("crypt32.dll").NewProc(proc).Call(uintptr(unsafe.Pointer(&input)), 0, 0, 0, 0, 1, uintptr(unsafe.Pointer(&output)))
	if result == 0 {
		return nil, err
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(uintptr(unsafe.Pointer(output.data)))
	return append([]byte(nil), unsafe.Slice(output.data, output.size)...), nil
}
func (v windowsVault) Load() ([]byte, error) {
	data, err := os.ReadFile(v.path)
	if err != nil {
		return nil, missing(err)
	}
	return protect(data, true)
}
func (v windowsVault) Save(data []byte) error {
	encrypted, err := protect(data, false)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(v.path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(v.path), ".jobadder-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(encrypted); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), v.path)
}
func (v windowsVault) Delete() error {
	err := os.Remove(v.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
