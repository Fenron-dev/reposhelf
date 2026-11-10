// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// SystemKeychain returns the Secret Service (GNOME Keyring, KWallet) via
// secret-tool, or nil when it is not installed (servers, Docker).
func SystemKeychain() Keychain {
	p, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil
	}
	return secretTool{path: p}
}

type secretTool struct{ path string }

func (s secretTool) run(stdin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (s secretTool) Get(host string) (string, error) {
	out, err := s.run("", "lookup", "service", KeychainService, "account", host)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "", nil // not found
	}
	return out, err
}

// Set passes the token on standard input.
func (s secretTool) Set(host, token string) error {
	_, err := s.run(token, "store", "--label=RepoShelf "+host, "service", KeychainService, "account", host)
	return err
}

func (s secretTool) Delete(host string) error {
	_, err := s.run("", "clear", "service", KeychainService, "account", host)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil
	}
	return err
}
