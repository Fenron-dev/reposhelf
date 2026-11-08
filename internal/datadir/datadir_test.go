// SPDX-License-Identifier: GPL-3.0-or-later

package datadir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// fixture is a fake machine: a profile dir, a program folder and a few
// candidate data folders, all inside t.TempDir().
type fixture struct {
	root, profile, exe string
	env                map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	// Resolve the temp dir once: on macOS it sits behind /var -> /private/var,
	// and the portable step follows symlinks of the executable.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		root:    root,
		profile: filepath.Join(root, "profile"),
		exe:     filepath.Join(root, "stick", "reposhelf"),
		env:     map[string]string{},
	}
	mkdir(t, f.profile)
	mkdir(t, filepath.Dir(f.exe))
	writeFile(t, f.exe, "binary")
	return f
}

func (f *fixture) Env(flag string) Env {
	return Env{
		Flag: flag,
		LookupEnv: func(k string) (string, bool) {
			v, ok := f.env[k]
			return v, ok
		},
		UserConfigDir: func() (string, error) { return f.profile, nil },
		Executable:    func() (string, error) { return f.exe, nil },
	}
}

func (f *fixture) dir(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.root, name)
	mkdir(t, p)
	return p
}

func (f *fixture) setPointer(t *testing.T, content string) {
	t.Helper()
	p := PointerPath(f.profile)
	mkdir(t, filepath.Dir(p))
	writeFile(t, p, content)
}

func (f *fixture) portableDir(t *testing.T) string {
	t.Helper()
	p := filepath.Join(filepath.Dir(f.exe), PortableDirName)
	mkdir(t, p)
	return p
}

func TestResolveOrder(t *testing.T) {
	t.Run("flag wins over everything", func(t *testing.T) {
		f := newFixture(t)
		want := f.dir(t, "flag")
		f.env[EnvVar] = f.dir(t, "env")
		f.setPointer(t, f.dir(t, "chosen"))
		f.portableDir(t)

		assertLocation(t, f.Env(want), want, SourceFlag)
	})
	t.Run("env wins over pointer and portable", func(t *testing.T) {
		f := newFixture(t)
		want := f.dir(t, "env")
		f.env[EnvVar] = want
		f.setPointer(t, f.dir(t, "chosen"))
		f.portableDir(t)

		assertLocation(t, f.Env(""), want, SourceEnv)
	})
	t.Run("empty env is treated as unset", func(t *testing.T) {
		f := newFixture(t)
		f.env[EnvVar] = ""
		want := f.dir(t, "chosen")
		f.setPointer(t, want+"\n")

		assertLocation(t, f.Env(""), want, SourcePointer)
	})
	t.Run("pointer wins over portable", func(t *testing.T) {
		f := newFixture(t)
		want := f.dir(t, "chosen")
		f.setPointer(t, "  "+want+"  \n")
		f.portableDir(t)

		assertLocation(t, f.Env(""), want, SourcePointer)
	})
	t.Run("portable next to program", func(t *testing.T) {
		f := newFixture(t)
		want := f.portableDir(t)

		assertLocation(t, f.Env(""), want, SourcePortable)
	})
	t.Run("nothing configured", func(t *testing.T) {
		f := newFixture(t)
		_, err := Resolve(f.Env(""))
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
	})
	t.Run("no profile dir skips pointer step", func(t *testing.T) {
		f := newFixture(t)
		want := f.portableDir(t)
		env := f.Env("")
		env.UserConfigDir = func() (string, error) { return "", errors.New("no $HOME") }

		assertLocation(t, env, want, SourcePortable)
	})
	t.Run("relative flag is made absolute", func(t *testing.T) {
		f := newFixture(t)
		want := f.dir(t, "rel")
		t.Chdir(f.root)

		assertLocation(t, f.Env("rel"), want, SourceFlag)
	})
}

// Invariant 5: a named but unreachable folder is an error, never a reason to
// fall back to the next step.
func TestResolveNeverFallsBack(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, f *fixture) Env
		source  Source
		wantErr error
	}{
		{
			name: "missing flag dir",
			setup: func(t *testing.T, f *fixture) Env {
				f.env[EnvVar] = f.dir(t, "env")
				return f.Env(filepath.Join(f.root, "gone"))
			},
			source: SourceFlag, wantErr: fs.ErrNotExist,
		},
		{
			name: "env points to a file",
			setup: func(t *testing.T, f *fixture) Env {
				p := filepath.Join(f.root, "file")
				writeFile(t, p, "x")
				f.env[EnvVar] = p
				f.portableDir(t)
				return f.Env("")
			},
			source: SourceEnv, wantErr: ErrNotDir,
		},
		{
			name: "pointer to missing dir (unplugged drive)",
			setup: func(t *testing.T, f *fixture) Env {
				f.setPointer(t, filepath.Join(f.root, "usb", "RepoShelf"))
				f.portableDir(t)
				return f.Env("")
			},
			source: SourcePointer, wantErr: fs.ErrNotExist,
		},
		{
			name: "empty pointer",
			setup: func(t *testing.T, f *fixture) Env {
				f.setPointer(t, " \n")
				f.portableDir(t)
				return f.Env("")
			},
			source: SourcePointer, wantErr: ErrPointerEmpty,
		},
		{
			name: "relative pointer",
			setup: func(t *testing.T, f *fixture) Env {
				f.setPointer(t, "RepoShelf-Daten")
				f.portableDir(t)
				return f.Env("")
			},
			source: SourcePointer, wantErr: ErrPointerRelative,
		},
		{
			name: "portable name taken by a file",
			setup: func(t *testing.T, f *fixture) Env {
				writeFile(t, filepath.Join(filepath.Dir(f.exe), PortableDirName), "x")
				return f.Env("")
			},
			source: SourcePortable, wantErr: ErrNotDir,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			loc, err := Resolve(tt.setup(t, f))

			var ue *UnreachableError
			if !errors.As(err, &ue) {
				t.Fatalf("Resolve() = %+v, %v; want *UnreachableError", loc, err)
			}
			if ue.Source != tt.source {
				t.Errorf("source = %s, want %s", ue.Source, tt.source)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveFollowsExecutableSymlink(t *testing.T) {
	f := newFixture(t)
	want := f.portableDir(t)
	link := filepath.Join(f.dir(t, "bin"), "reposhelf")
	if err := os.Symlink(f.exe, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
	env := f.Env("")
	env.Executable = func() (string, error) { return link, nil }

	assertLocation(t, env, want, SourcePortable)
}

func TestPortableBase(t *testing.T) {
	tests := []struct {
		name, exe, want string
	}{
		{"plain binary", "/media/stick/reposhelf", "/media/stick"},
		{"macOS bundle", "/Volumes/Stick/RepoShelf.app/Contents/MacOS/reposhelf", "/Volumes/Stick"},
		{"bundle suffix case-insensitive", "/x/RepoShelf.APP/Contents/MacOS/reposhelf", "/x"},
		{"MacOS dir outside a bundle", "/x/notabundle/Contents/MacOS/reposhelf", "/x/notabundle/Contents/MacOS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PortableBase(filepath.FromSlash(tt.exe))
			if want := filepath.FromSlash(tt.want); got != want {
				t.Errorf("PortableBase(%q) = %q, want %q", tt.exe, got, want)
			}
		})
	}
}

func assertLocation(t *testing.T, env Env, wantPath string, wantSrc Source) {
	t.Helper()
	loc, err := Resolve(env)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if loc.Path != wantPath || loc.Source != wantSrc {
		t.Errorf("Resolve() = {%s %s}, want {%s %s}", loc.Path, loc.Source, wantPath, wantSrc)
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWritePointer(t *testing.T) {
	profile := t.TempDir()
	data := t.TempDir()
	if err := WritePointer(profile, "relative"); err == nil {
		t.Error("relative path accepted")
	}
	if err := WritePointer(profile, data); err != nil {
		t.Fatal(err)
	}
	loc, err := Resolve(Env{
		LookupEnv:     func(string) (string, bool) { return "", false },
		UserConfigDir: func() (string, error) { return profile, nil },
		Executable:    func() (string, error) { return "", os.ErrNotExist },
	})
	if err != nil || loc.Source != SourcePointer || loc.Path != data {
		t.Errorf("Resolve = %+v, %v", loc, err)
	}
}
