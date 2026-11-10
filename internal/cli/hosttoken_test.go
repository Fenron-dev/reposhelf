// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

type memKeychain map[string]string

func (m memKeychain) Get(h string) (string, error) { return m[h], nil }
func (m memKeychain) Set(h, t string) error        { m[h] = t; return nil }
func (m memKeychain) Delete(h string) error        { delete(m, h); return nil }

func TestHostToken(t *testing.T) {
	kc := memKeychain{}
	git := testutil.NewForge(t).Git
	dir := filepath.Join(t.TempDir(), "Daten")
	run := func(stdin string, args ...string) (int, string) {
		var out, errb bytes.Buffer
		code := run(args, system{stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin), keychain: kc, git: git,
			lookupEnv:     func(string) (string, bool) { return "", false },
			userConfigDir: func() (string, error) { return t.TempDir(), nil },
			executable:    func() (string, error) { return filepath.Join(dir, "x"), nil }})
		return code, out.String() + errb.String()
	}
	if code, out := run("", "init", dir); code != ExitOK {
		t.Fatalf("init: %s", out)
	}
	// A host without API: stored unchecked, no network.
	if code, out := run("glpat_token_123\n", "host-token", "set", "git.example.org", "--data", dir); code != ExitOK || !strings.Contains(out, "gespeichert") {
		t.Fatalf("set: %d %s", code, out)
	}
	if kc["git.example.org"] != "glpat_token_123" {
		t.Errorf("keychain = %v", kc)
	}
	if code, out := run("", "host-token", "list", "--data", dir); code != ExitOK || !strings.Contains(out, "github.com") || strings.Contains(out, "glpat") {
		t.Errorf("list: %s", out)
	}
	if code, out := run("not a token\n", "host-token", "set", "git.example.org", "--data", dir); code != ExitError || !strings.Contains(out, "nicht gespeichert") {
		t.Errorf("bad token: %d %s", code, out)
	}
	if code, out := run("", "host-token", "remove", "git.example.org", "--data", dir); code != ExitOK || kc["git.example.org"] != "" {
		t.Errorf("remove: %d %s", code, out)
	}

	// Local issues on a repository created here.
	if code, out := run("", "create", "werkzeug", "--data", dir); code != ExitOK {
		t.Fatalf("create: %s", out)
	}
	if code, out := run("Lange Beschreibung\nmit Zeilen", "issue", "new", "werkzeug", "--title", "Fehler beim Export", "--body", "-", "--labels", "bug", "--data", dir); code != ExitOK || !strings.Contains(out, "L1 angelegt") {
		t.Fatalf("issue new: %d %s", code, out)
	}
	if code, out := run("", "issue", "close", "werkzeug", "L1", "--body", "behoben", "--data", dir); code != ExitOK {
		t.Errorf("issue close: %s", out)
	}
	if _, out := run("", "issue", "list", "werkzeug", "--data", dir); strings.Contains(out, "L1") {
		t.Errorf("closed issue listed: %s", out)
	}
	if _, out := run("", "issue", "list", "werkzeug", "--all", "--data", dir); !strings.Contains(out, "L1    zu    Fehler beim Export [bug]") {
		t.Errorf("issue list --all: %q", out)
	}
}
