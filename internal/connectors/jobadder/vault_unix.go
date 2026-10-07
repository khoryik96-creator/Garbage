//go:build !windows && !darwin

package jobadder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type systemVault struct{ account string }

func NewVault(directory string) Vault {
	return systemVault{fmt.Sprintf("%x", sha256.Sum256([]byte(directory)))}
}
func (v systemVault) Available() bool {
	binary := "secret-tool"
	_, err := exec.LookPath(binary)
	return err == nil
}
func (v systemVault) command(action string, data []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	args := map[string][]string{"load": {"lookup", "application", "garbage-truck", "workspace", v.account}, "save": {"store", "--label=Garbage Truck JobAdder", "application", "garbage-truck", "workspace", v.account}, "delete": {"clear", "application", "garbage-truck", "workspace", v.account}}[action]
	command = exec.CommandContext(ctx, "secret-tool", args...)
	if action == "save" {
		command.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(data) + "\n")
	}
	return command.Output()
}
func (v systemVault) Load() ([]byte, error) {
	if !v.Available() {
		return nil, ErrNoCredentials
	}
	data, err := v.command("load", nil)
	if err != nil {
		var exit *exec.ExitError
		// secret-tool reports a no-match with exit 1 and no diagnostic. D-Bus,
		// unlock and command failures carry diagnostics and must remain failures.
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
			return nil, ErrNoCredentials
		}
		return nil, fmt.Errorf("protected credential lookup failed")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
}
func (v systemVault) Save(data []byte) error { _, err := v.command("save", data); return err }
func (v systemVault) Delete() error          { _, err := v.command("delete", nil); return err }
