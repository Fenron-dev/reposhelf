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
		{name: "profile unknown os", file: "release_profiles:\n  - name: A\n    os: [plan9]\n", wantErr: ErrInvalid},
		{name: "profile twice", file: "release_profiles:\n  - name: A\n  - name: A\n", wantErr: ErrInvalid},
		{name: "profile named Standard", file: "release_profiles:\n  - name: Standard\n", wantErr: ErrInvalid},
		{name: "profile empty name", file: "release_profiles:\n  - os: [linux]\n", wantErr: ErrInvalid},
		{name: "default profile unknown", file: "default_release_profile: B\n", wantErr: ErrInvalid},
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
	// Files of earlier versions have no updates section: the check is on.
	if !res.Config.Updates.Check {
		t.Error("update check off for a file without the updates section")
	}
	off, err := Load(dataDirWith(t, "updates:\n  check: false\n"), noEnv)
	if err != nil || off.Config.Updates.Check {
		t.Errorf("check: false not read: %+v %v", off.Config.Updates, err)
	}
}

func ptr(s string) *string { return &s }

func TestSaveKeepsUnknownAndComments(t *testing.T) {
	dir := t.TempDir()
	raw := "# meine Einstellungen\nschema: 1\nserver:\n  listen: \"0.0.0.0:7373\"\nfuture: [x]\n"
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Load(dir, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	cfg := l.Config
	cfg.Retention.ReleasesKeep = 7
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, FileName))
	s := string(b)
	if !strings.Contains(s, "# meine Einstellungen") || !strings.Contains(s, "future: [x]") || !strings.Contains(s, "releases_keep: 7") {
		t.Errorf("saved = %s", s)
	}
	l2, err := Load(dir, func(string) (string, bool) { return "", false })
	if err != nil || l2.Config.Retention.ReleasesKeep != 7 || l2.Config.Sync.DefaultInterval == 0 {
		t.Errorf("reload = %+v, %v", l2, err)
	}
	bad := cfg
	bad.Sync.Concurrency = 0
	if err := Save(dir, bad); err == nil {
		t.Error("invalid config saved")
	}
	// New file in an empty folder.
	if err := Save(t.TempDir(), Default()); err != nil {
		t.Error(err)
	}
}

// TestReleaseProfiles: profiles load, Standard always exists (D-035).
func TestReleaseProfiles(t *testing.T) {
	raw := "release_profiles:\n  - name: Desktop\n    releases_keep: 2\n    os: [windows, linux]\n    arch: [x64]\n    formats: [linux/appimage]\ndefault_release_profile: Desktop\n"
	l, err := Load(dataDirWith(t, raw), noEnv)
	if err != nil {
		t.Fatal(err)
	}
	c := l.Config
	p, ok := c.Profile("Desktop")
	if !ok || *p.ReleasesKeep != 2 || len(p.Filter().OS) != 2 || c.DefaultReleaseProfile != "Desktop" {
		t.Errorf("profile = %+v %v", p, ok)
	}
	if s, ok := c.Profile(""); !ok || s.Name != StandardProfile || !s.Filter().Empty() {
		t.Errorf("standard = %+v", s)
	}
	if _, ok := c.Profile("Nope"); ok {
		t.Error("unknown profile found")
	}
	if n := c.ProfileNames(); len(n) != 2 || n[0] != StandardProfile {
		t.Errorf("names = %v", n)
	}
	if len(l.Warnings) != 0 {
		t.Errorf("warnings = %v", l.Warnings)
	}
}

// TestSaveProfilesKeepUnknown: unknown keys inside a profile follow it
// when profiles are removed or reordered.
func TestSaveProfilesKeepUnknown(t *testing.T) {
	raw := "release_profiles:\n  - name: A\n    future: 1\n  - name: B\n    other: 2\n"
	dir := dataDirWith(t, raw)
	l, err := Load(dir, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := l.Config
	cfg.ReleaseProfiles = cfg.ReleaseProfiles[1:]
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, FileName))
	if !strings.Contains(string(out), "other: 2") || strings.Contains(string(out), "future: 1") {
		t.Errorf("saved:\n%s", out)
	}
}
