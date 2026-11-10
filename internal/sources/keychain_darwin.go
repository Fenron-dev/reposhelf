// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const securityTool = "/usr/bin/security"

// SystemKeychain returns the macOS login keychain.
func SystemKeychain() Keychain { return macKeychain{} }

type macKeychain struct{}

func (macKeychain) Get(host string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, securityTool, "find-generic-password", "-s", KeychainService, "-a", host, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 { // not found
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Set passes the token on standard input ("security -i"), so it never
// shows up in the process list.
func (k macKeychain) Set(host, token string) error {
	if !ValidToken(token) || !validHost(host) {
		return ErrInvalidToken
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, securityTool, "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", KeychainService, host, token))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %v %s", err, strings.TrimSpace(string(out)))
	}
	// "security -i" exits 0 even when a command fails: read it back.
	if got, err := k.Get(host); err != nil || got != token {
		return errors.New("keychain: token was not stored")
	}
	return nil
}

func (macKeychain) Delete(host string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, securityTool, "delete-generic-password", "-s", KeychainService, "-a", host).Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 44 {
		return nil
	}
	return err
}
