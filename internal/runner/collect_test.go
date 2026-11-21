// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
)

// TestCollectStaysInside: links the build creates must not pull files of
// this machine into the artifacts (R-015).
func TestCollectStaysInside(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "geheim")
	os.MkdirAll(secret, 0o755)
	os.WriteFile(filepath.Join(secret, "instance.key"), []byte("private"), 0o600)

	work := t.TempDir()
	os.MkdirAll(filepath.Join(work, "dist"), 0o755)
	os.WriteFile(filepath.Join(work, "dist", "app"), []byte("binary"), 0o644)
	os.Symlink(secret, filepath.Join(work, "dist", "outside"))         // dir link out
	os.Symlink(secret, filepath.Join(work, "out"))                     // pattern root out
	os.Symlink(filepath.Join(work, "dist"), filepath.Join(work, "in")) // link inside: fine

	b := &artifact.Build{Dir: t.TempDir()}
	m := &Manager{}
	if err := m.collect(b, work, []string{"dist/*", "out/*", "in/*"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range b.Artifacts {
		names = append(names, a.Name)
		if a.Name == "dist/outside/instance.key" || a.Name == "out/instance.key" {
			t.Errorf("file of the host collected: %s", a.Name)
		}
	}
	if len(names) != 2 { // dist/app and in/app
		t.Errorf("artifacts = %v", names)
	}
}
