// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Keychain service name used on macOS and Linux (secret-tool).
const KeychainService = "RepoShelf"

// EnvTokens looks up tokens for a host, in this order:
//
//  1. REPOSHELF_TOKEN_<HOST> (e.g. REPOSHELF_TOKEN_GITHUB_COM), or
//     REPOSHELF_TOKEN_<HOST>_FILE naming a file (Docker secrets),
//  2. for github.com: REPOSHELF_GITHUB_TOKEN, GITHUB_TOKEN, GH_TOKEN (and
//     REPOSHELF_GITHUB_TOKEN_FILE),
//  3. the system keychain (macOS Keychain, Linux Secret Service) under the
//     service "RepoShelf" with the host as account.
//
// Tokens are never stored in the data folder (KONZEPT 4.2).
func EnvTokens(lookupEnv func(string) (string, bool), useKeychain bool) Tokens {
	cache := map[string]string{}
	return func(host string) string {
		if t, ok := cache[host]; ok {
			return t
		}
		t := lookupToken(lookupEnv, host, useKeychain)
		cache[host] = t
		return t
	}
}

func lookupToken(lookupEnv func(string) (string, bool), host string, useKeychain bool) string {
	name := "REPOSHELF_TOKEN_" + envName(host)
	names := []string{name}
	if strings.EqualFold(host, "github.com") {
		names = append(names, "REPOSHELF_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN")
	}
	for _, n := range names {
		if v, ok := lookupEnv(n); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if f, ok := lookupEnv(n + "_FILE"); ok && f != "" {
			if b, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(b)) != "" {
				return strings.TrimSpace(string(b))
			}
		}
	}
	if useKeychain {
		return keychain(host)
	}
	return ""
}

// envName turns "gitea.local:3000" into "GITEA_LOCAL_3000".
func envName(host string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return '_'
	}, host)
}

func keychain(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", KeychainService, "-a", host, "-w")
	case "linux":
		p, err := exec.LookPath("secret-tool")
		if err != nil {
			return ""
		}
		cmd = exec.CommandContext(ctx, p, "lookup", "service", KeychainService, "account", host)
	default:
		return ""
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
