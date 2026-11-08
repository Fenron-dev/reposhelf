// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
)

// fakeOS is a machine without any RepoShelf setup unless a test adds one.
type fakeOS struct {
	env            map[string]string
	profile        string
	stdout, stderr bytes.Buffer
}

func newFakeOS(t *testing.T) *fakeOS {
	t.Helper()
	root := t.TempDir()
	return &fakeOS{
		env:     map[string]string{},
		profile: filepath.Join(root, "profile"),
	}
}

func (f *fakeOS) run(args ...string) int {
	return run(args, system{
		stdout: &f.stdout,
		stderr: &f.stderr,
		lookupEnv: func(k string) (string, bool) {
			v, ok := f.env[k]
			return v, ok
		},
		userConfigDir: func() (string, error) { return f.profile, nil },
		executable:    func() (string, error) { return "", errors.New("unknown in tests") },
	})
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring, empty = expect no output
		wantStderr string // substring, empty = expect no output
	}{
		{"no args shows usage", nil, ExitOK, "Aufruf:", ""},
		{"help", []string{"help"}, ExitOK, "Aufruf:", ""},
		{"--help", []string{"--help"}, ExitOK, "Aufruf:", ""},
		{"version", []string{"version"}, ExitOK, "reposhelf dev", ""},
		{"--version", []string{"--version"}, ExitOK, "reposhelf dev", ""},
		{"unknown command", []string{"frobnicate"}, ExitUsage, "", `Unbekannter Befehl: "frobnicate"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeOS(t)
			code := f.run(tt.args...)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			checkOutput(t, "stdout", f.stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", f.stderr.String(), tt.wantStderr)
		})
	}
}

func TestConfigCommand(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		f := newFakeOS(t)
		code := f.run("config")
		if code != ExitError {
			t.Errorf("exit code = %d, want %d", code, ExitError)
		}
		checkOutput(t, "stdout", f.stdout.String(), "")
		checkOutput(t, "stderr", f.stderr.String(), "Kein Datenordner eingerichtet")
	})

	t.Run("data dir from flag, defaults", func(t *testing.T) {
		f := newFakeOS(t)
		dir := t.TempDir()
		code := f.run("config", "--data", dir)
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, f.stderr.String())
		}
		out := f.stdout.String()
		for _, want := range []string{dir, "Option --data", "nicht vorhanden", "127.0.0.1:7373"} {
			checkOutput(t, "stdout", out, want)
		}
	})

	t.Run("data dir from env, file and env override", func(t *testing.T) {
		f := newFakeOS(t)
		dir := t.TempDir()
		writeConfig(t, dir, "server:\n  listen: \"0.0.0.0:8000\"\n  typo: 1\n")
		f.env[datadir.EnvVar] = dir
		f.env[config.EnvListen] = ":7373"

		if code := f.run("config"); code != ExitOK {
			t.Fatalf("exit code = %d, stderr = %q", code, f.stderr.String())
		}
		checkOutput(t, "stdout", f.stdout.String(), "Umgebungsvariable REPOSHELF_DATA")
		checkOutput(t, "stdout", f.stdout.String(), "Adresse:        :7373")
		checkOutput(t, "stderr", f.stderr.String(), "Warnung in reposhelf.yaml")
	})

	t.Run("unreachable data dir does not fall back", func(t *testing.T) {
		f := newFakeOS(t)
		f.env[datadir.EnvVar] = t.TempDir() // valid, but must lose to the flag
		missing := filepath.Join(t.TempDir(), "usb")

		if code := f.run("config", "--data", missing); code != ExitError {
			t.Errorf("exit code = %d, want %d", code, ExitError)
		}
		checkOutput(t, "stdout", f.stdout.String(), "")
		for _, want := range []string{"nicht erreichbar", missing, "existiert nicht", "weicht nicht"} {
			checkOutput(t, "stderr", f.stderr.String(), want)
		}
	})

	t.Run("invalid config", func(t *testing.T) {
		f := newFakeOS(t)
		dir := t.TempDir()
		writeConfig(t, dir, "schema: 99\n")

		if code := f.run("config", "--data", dir); code != ExitError {
			t.Errorf("exit code = %d, want %d", code, ExitError)
		}
		checkOutput(t, "stderr", f.stderr.String(), "Konfiguration fehlerhaft")
	})

	t.Run("extra argument", func(t *testing.T) {
		f := newFakeOS(t)
		if code := f.run("config", "surplus"); code != ExitUsage {
			t.Errorf("exit code = %d, want %d", code, ExitUsage)
		}
		checkOutput(t, "stderr", f.stderr.String(), `Unerwartete Angabe: "surplus"`)
	})

	t.Run("unknown flag", func(t *testing.T) {
		f := newFakeOS(t)
		if code := f.run("config", "--nope"); code != ExitUsage {
			t.Errorf("exit code = %d, want %d", code, ExitUsage)
		}
	})
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want empty", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}
