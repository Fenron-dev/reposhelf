// SPDX-License-Identifier: GPL-3.0-or-later

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.7.2", "v0.7.1", true}, {"v0.8.0", "v0.7.9", true}, {"v1.0.0", "v0.99.99", true},
		{"v0.7.1", "v0.7.1", false}, {"v0.7.0", "v0.7.1", false},
		{"v0.7.1", "v0.7.1-rc1", true}, {"v0.7.1-rc1", "v0.7.1", false},
		{"v0.7.2", "dev", false}, {"nightly", "v0.1.0", false}, {"v1.2", "v1.1.0", false},
		{"v0.7.2", "0.7.1", true}, {"v0.7.1", "0.7.1", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestDetectFlavor(t *testing.T) {
	if DetectFlavor("0.7.1", false, false) != FlavorCLI || DetectFlavor("nightly", false, false) != FlavorDev {
		t.Error("release builds carry the version without v")
	}
	if DetectFlavor("dev", false, false) != FlavorDev || DetectFlavor("v1.0.0", false, true) != FlavorDocker || DetectFlavor("v1.0.0", false, false) != FlavorCLI {
		t.Error("flavor")
	}
}

// fakeRelease serves a GitHub API answer and the release files.
type fakeRelease struct {
	srv   *httptest.Server
	files map[string][]byte
	tag   string
	hits  int
}

func newFake(t *testing.T, tag string, files map[string][]byte, sums bool) *fakeRelease {
	f := &fakeRelease{files: files, tag: tag}
	if sums {
		var b strings.Builder
		for name, data := range files {
			h := sha256.Sum256(data)
			b.WriteString(hex.EncodeToString(h[:]) + "  " + name + "\n")
		}
		files[SumsName] = []byte(b.String())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		var assets []map[string]any
		for name, data := range f.files {
			assets = append(assets, map[string]any{"name": name, "size": len(data), "browser_download_url": f.srv.URL + "/dl/" + name})
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "html_url": "https://example.test/r", "published_at": "2026-12-01T10:00:00Z", "assets": assets})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		data, ok := f.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func tarGz(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: filepath.Dir(name) + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4})
	tw.Write([]byte("evil"))
	tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data))})
	tw.Write(data)
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	w.Write(data)
	zw.Close()
	return buf.Bytes()
}

func newUpdater(t *testing.T, f *fakeRelease, flavor Flavor) *Updater {
	dir := t.TempDir()
	exe := filepath.Join(dir, "reposhelf")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	return &Updater{Current: "v0.7.1", Flavor: flavor, Exe: exe, StatePath: filepath.Join(t.TempDir(), StateFile),
		API: f.srv.URL, Client: f.srv.Client(), GOOS: "linux", GOARCH: "amd64",
		Trash: func(string) error { t.Fatal("trash used"); return nil }}
}

func TestCheckAndInstallCLI(t *testing.T) {
	ctx := context.Background()
	f := newFake(t, "v0.7.2", map[string][]byte{
		"reposhelf-0.7.2-linux-amd64.tar.gz": tarGz(t, "reposhelf-0.7.2-linux-amd64/reposhelf", []byte("NEW")),
		"reposhelf-0.7.2-linux-arm64.tar.gz": []byte("other"),
	}, true)
	u := newUpdater(t, f, FlavorCLI)
	var reported []string
	u.OnNew = func(r Release) { reported = append(reported, r.Tag) }
	if _, err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	u.Check(ctx)
	if len(reported) != 1 || reported[0] != "v0.7.2" {
		t.Errorf("reported %v, want once", reported)
	}
	if rel, ok := u.Available(); !ok || rel.Tag != "v0.7.2" {
		t.Fatal("no update available")
	}
	msg, err := u.Install(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "NEW" {
		t.Errorf("exe = %q", b)
	}
	if b, _ := os.ReadFile(u.Exe + ".old"); string(b) != "OLD" {
		t.Errorf("backup = %q", b)
	}
	if !strings.Contains(msg, "v0.7.2") || u.State().Installed != "v0.7.2" {
		t.Errorf("msg %q state %+v", msg, u.State())
	}
	entries, _ := os.ReadDir(filepath.Dir(u.Exe))
	if len(entries) != 2 {
		t.Errorf("left in the program folder: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(u.Exe)), "evil")); err == nil {
		t.Error("archive wrote outside")
	}
	// After a restart into v0.7.2 nothing is pending or available.
	u.Current = "v0.7.2"
	if _, ok := u.Available(); ok || u.State().Installed != "" {
		t.Error("still pending after restart")
	}
}

func TestInstallRefusesBadChecksum(t *testing.T) {
	ctx := context.Background()
	good := tarGz(t, "reposhelf-0.7.2-linux-amd64/reposhelf", []byte("NEW"))
	f := newFake(t, "v0.7.2", map[string][]byte{"reposhelf-0.7.2-linux-amd64.tar.gz": good}, true)
	f.files["reposhelf-0.7.2-linux-amd64.tar.gz"] = tarGz(t, "reposhelf-0.7.2-linux-amd64/reposhelf", []byte("BAD"))
	u := newUpdater(t, f, FlavorCLI)
	u.Check(ctx)
	if _, err := u.Install(ctx); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "OLD" {
		t.Error("exe replaced despite a wrong checksum")
	}
	if entries, _ := os.ReadDir(filepath.Dir(u.Exe)); len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}

	// Without any checksum nothing is installed either.
	f2 := newFake(t, "v0.7.2", map[string][]byte{"reposhelf-0.7.2-linux-amd64.tar.gz": good}, false)
	u2 := newUpdater(t, f2, FlavorCLI)
	u2.Check(ctx)
	if _, err := u2.Install(ctx); err == nil {
		t.Error("installed without checksum")
	}
}

func TestInstallWindowsAppAndBackup(t *testing.T) {
	ctx := context.Background()
	f := newFake(t, "v0.8.0", map[string][]byte{"RepoShelf-Windows-portable.zip": zipOf(t, "RepoShelf/RepoShelf.exe", []byte("APP"))}, true)
	u := newUpdater(t, f, FlavorWindowsApp)
	os.WriteFile(u.Exe+".old", []byte("OLDER"), 0o755)
	var trashed []string
	u.Trash = func(p string) error { trashed = append(trashed, p); return os.Rename(p, p+".trashed") }
	u.Check(ctx)
	if _, err := u.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "APP" || len(trashed) != 1 {
		t.Errorf("exe %q trashed %v", b, trashed)
	}
}

func TestInstallMacDMG(t *testing.T) {
	ctx := context.Background()
	dmg := []byte("DMG")
	h := sha256.Sum256(dmg)
	f := newFake(t, "v0.8.0", map[string][]byte{"RepoShelf-macOS.dmg": dmg, "RepoShelf-macOS.dmg.sha256": []byte(hex.EncodeToString(h[:]) + "\n")}, false)
	u := newUpdater(t, f, FlavorMacApp)
	u.DownloadDir = t.TempDir()
	os.WriteFile(filepath.Join(u.DownloadDir, "RepoShelf-v0.8.0.dmg"), []byte("vorhanden"), 0o644)
	var opened string
	u.Open = func(p string) error { opened = p; return nil }
	u.Check(ctx)
	if _, err := u.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(opened) != "RepoShelf-v0.8.0 (2).dmg" {
		t.Errorf("opened %q", opened)
	}
	if b, _ := os.ReadFile(opened); string(b) != "DMG" {
		t.Errorf("dmg = %q", b)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "OLD" {
		t.Error("mac app replaced an executable")
	}
}

func TestRunRespectsSwitchAndDay(t *testing.T) {
	f := newFake(t, "v0.7.1", map[string][]byte{}, false)
	u := newUpdater(t, f, FlavorDocker)
	on := false
	u.Enabled = func() bool { return on }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	u.Run(ctx)
	if f.hits != 0 {
		t.Errorf("checked while switched off: %d", f.hits)
	}
	on = true
	u.Check(context.Background())
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	u.Run(ctx2)
	if f.hits != 1 {
		t.Errorf("checked %d times within a day, want 1", f.hits)
	}
	if u.CanInstall() {
		t.Error("docker can install")
	}
}
