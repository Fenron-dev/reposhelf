// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) (string, bool) { return "", false }

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func dataDirWith(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDefault(t *testing.T) {
	if got, want := Default().Server.Listen, "127.0.0.1:7373"; got != want {
		t.Errorf("default listen = %q, want %q", got, want)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name         string
		file         *string // nil = no file
		env          map[string]string
		wantListen   string
		wantWarnings []string // substrings, one per warning
	}{
		{name: "no file uses defaults", wantListen: "127.0.0.1:7373"},
		{name: "empty file uses defaults", file: ptr(""), wantListen: "127.0.0.1:7373"},
		{name: "comments only", file: ptr("# nothing\n"), wantListen: "127.0.0.1:7373"},
		{name: "file overrides default", file: ptr("schema: 1\nserver:\n  listen: \"0.0.0.0:8000\"\n"), wantListen: "0.0.0.0:8000"},
		{name: "schema may be omitted", file: ptr("server:\n  listen: \":9000\"\n"), wantListen: ":9000"},
		{name: "null section keeps defaults", file: ptr("server:\n"), wantListen: "127.0.0.1:7373"},
		{
			name:       "env overrides file",
			file:       ptr("server:\n  listen: \"0.0.0.0:8000\"\n"),
			env:        map[string]string{EnvListen: ":7373"},
			wantListen: ":7373",
		},
		{
			name:       "empty env is ignored",
			env:        map[string]string{EnvListen: ""},
			wantListen: "127.0.0.1:7373",
		},
		{
			name:         "unknown keys are warnings",
			file:         ptr("schema: 1\nsevrer:\n  listen: x\nserver:\n  listen: \":8000\"\n  tls: true\n"),
			wantListen:   ":8000",
			wantWarnings: []string{"sevrer", "tls"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != nil {
				dir = dataDirWith(t, *tt.file)
			}
			res, err := Load(dir, envOf(tt.env))
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if res.FileExists != (tt.file != nil) {
				t.Errorf("FileExists = %v, want %v", res.FileExists, tt.file != nil)
			}
			if res.Config.Schema != CurrentSchema {
				t.Errorf("Schema = %d, want %d", res.Config.Schema, CurrentSchema)
			}
			if res.Config.Server.Listen != tt.wantListen {
				t.Errorf("Listen = %q, want %q", res.Config.Server.Listen, tt.wantListen)
			}
			if len(res.Warnings) != len(tt.wantWarnings) {
				t.Fatalf("Warnings = %q, want %d warnings", res.Warnings, len(tt.wantWarnings))
			}
			for i, w := range tt.wantWarnings {
				if !strings.Contains(res.Warnings[i], w) {
					t.Errorf("warning %d = %q, want it to mention %q", i, res.Warnings[i], w)
				}
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		env     map[string]string
		wantErr error // nil = any error
	}{
		{name: "newer schema", file: "schema: 2\n", wantErr: ErrNewerSchema},
		{name: "negative schema", file: "schema: -1\n", wantErr: ErrInvalid},
		{name: "missing port", file: "server:\n  listen: \"127.0.0.1\"\n", wantErr: ErrInvalid},
		{name: "explicitly empty listen", file: "server:\n  listen: \"\"\n", wantErr: ErrInvalid},
		{name: "port not a number", file: "server:\n  listen: \":http\"\n", wantErr: ErrInvalid},
		{name: "port zero", file: "server:\n  listen: \":0\"\n", wantErr: ErrInvalid},
		{name: "port too high", file: "server:\n  listen: \":70000\"\n", wantErr: ErrInvalid},
		{name: "invalid env value", env: map[string]string{EnvListen: "nonsense"}, wantErr: ErrInvalid},
		{name: "malformed yaml", file: "server: [unclosed\n"},
		{name: "wrong type", file: "schema: eins\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(dataDirWith(t, tt.file), envOf(tt.env))
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	// A directory named reposhelf.yaml cannot be read as a file. This must be
	// an error, not a silent fallback to defaults.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, noEnv); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

// The schema-1 fixture must keep loading in every later version.
func TestLoadSchema1Fixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "schema1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Load(dataDirWith(t, string(raw)), noEnv)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if res.Config.Server.Listen != "0.0.0.0:7373" {
		t.Errorf("Listen = %q, want %q", res.Config.Server.Listen, "0.0.0.0:7373")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", res.Warnings)
	}
}

func ptr(s string) *string { return &s }
