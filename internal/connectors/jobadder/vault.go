package jobadder

import (
	"errors"
	"os"
)

// Vault stores secrets outside SQLite so workspace exports contain no credentials.
// Unsupported/locked OS vaults fail closed; session-only use is explicit.
type Vault interface {
	Load() ([]byte, error)
	Save([]byte) error
	Delete() error
	Available() bool
}

var ErrNoCredentials = errors.New("no saved JobAdder credentials")

func missing(err error) error {
	if os.IsNotExist(err) {
		return ErrNoCredentials
	}
	return err
}
