package jobadder

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCredentialsEncryptedAndReloadable(t *testing.T) {
	dir := t.TempDir()
	vault := NewVault(dir)
	secret := []byte("test-only-client-secret-and-token")
	if err := vault.Save(secret); err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(dir, "jobadder.dpapi"))
	if err != nil || bytes.Contains(encrypted, secret) {
		t.Fatal("plaintext credential storage", err)
	}
	data, err := vault.Load()
	if err != nil || !bytes.Equal(data, secret) {
		t.Fatal("DPAPI round trip failed", err)
	}
	if err = vault.Delete(); err != nil {
		t.Fatal(err)
	}
}
