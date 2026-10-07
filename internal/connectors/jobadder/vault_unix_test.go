//go:build !windows && !darwin

package jobadder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretServiceLookupDistinguishesMissingAndUnavailable(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "secret-tool")
	t.Setenv("PATH", directory)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	vault := NewVault(directory)
	if _, err := vault.Load(); !errors.Is(err, ErrNoCredentials) {
		t.Fatal("no-match was not recognized", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'D-Bus keyring unavailable' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Load(); err == nil || errors.Is(err, ErrNoCredentials) {
		t.Fatal("operational failure was hidden", err)
	}
	c := NewConnection(vault)
	defer c.Close()
	if !c.Status().Saved || c.Status().Error == "" {
		t.Fatal("inaccessible saved account was hidden")
	}
	cfg := Configuration{ClientID: "new", ClientSecret: "secret", RedirectURI: callbackAddress(t)}
	if err := c.Configure(cfg); err == nil {
		t.Fatal("session mode skipped removal of inaccessible saved credentials")
	}
	if err := c.Disconnect(); err == nil {
		t.Fatal("disconnect skipped inaccessible saved credentials")
	}
}
