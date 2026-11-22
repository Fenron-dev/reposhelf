// SPDX-License-Identifier: GPL-3.0-or-later

package assetclass

import (
	"slices"
	"strings"
	"testing"
)

// Names from real releases (PhotoCraft, Electron, Obsidian, Ollama, Zed,
// ripgrep).
func TestClassify(t *testing.T) {
	cases := map[string]Class{
		"photocraft-0.23.1-linux-x86_64.AppImage":             {Linux, X64, AppImage},
		"photocraft-0.23.1-linux-aarch64.deb":                 {Linux, ARM64, Deb},
		"photocraft-0.23.1-linux-riscv64.rpm":                 {Linux, RISCV64, RPM},
		"photocraft-0.23.1-linux-x86_64.pkg.tar.zst":          {Linux, X64, ArchPkg},
		"photocraft-0.23.1-linux-x86_64.flatpak":              {Linux, X64, Flatpak},
		"photocraft-0.23.1-freebsd-x86_64.tar.gz":             {BSD, X64, Archive},
		"photocraft-0.23.1-macos-universal.dmg":               {MacOS, Universal, DMG},
		"photocraft-0.23.1-windows-x64.msi":                   {Windows, X64, MSI},
		"photocraft-0.23.1-windows-arm64-portable.zip":        {Windows, ARM64, Archive},
		"photocraft-0.23.1-windows-x64-symbols.zip":           {Windows, X64, Symbols},
		"photocraft-web-0.23.1.zip":                           {Any, "", Archive},
		"SHA256SUMS.txt":                                      {Any, "", Other},
		"electron-v46.0.0-alpha.12-win32-x64.zip":             {Windows, X64, Archive},
		"electron-v46.0.0-alpha.12-win32-arm64-pdb.zip":       {Windows, ARM64, Symbols},
		"electron-v46.0.0-alpha.12-darwin-arm64-dsym.tar.xz":  {MacOS, ARM64, Symbols},
		"electron-v46.0.0-alpha.12-mas-x64.zip":               {MacOS, X64, Archive},
		"electron-v46.0.0-alpha.12-linux-x64-debug.zip":       {Linux, X64, Symbols},
		"Obsidian-1.15.1.AppImage":                            {Linux, "", AppImage},
		"Obsidian-1.15.1-arm64.AppImage":                      {Linux, ARM64, AppImage},
		"Obsidian-1.15.1.exe":                                 {Windows, "", EXE},
		"Obsidian-1.15.1.dmg":                                 {MacOS, "", DMG},
		"Obsidian-1.15.1.apk":                                 {Android, "", APK},
		"obsidian_1.15.1_amd64.deb":                           {Linux, X64, Deb},
		"OllamaSetup.exe":                                     {Windows, "", EXE},
		"Ollama-darwin.zip":                                   {MacOS, "", Archive},
		"ollama-linux-amd64-rocm.tar.zst":                     {Linux, X64, Archive},
		"install.ps1":                                         {Windows, "", Script},
		"install.sh":                                          {Any, "", Script},
		"Zed-aarch64.dmg":                                     {MacOS, ARM64, DMG},
		"zed-remote-server-windows-x86_64.zip":                {Windows, X64, Archive},
		"ripgrep-15.2.0-aarch64-apple-darwin.tar.gz":          {MacOS, ARM64, Archive},
		"ripgrep-15.2.0-i686-pc-windows-msvc.zip":             {Windows, X86, Archive},
		"ripgrep-15.2.0-armv7-unknown-linux-gnueabihf.tar.gz": {Linux, ARM, Archive},
		"ripgrep-15.2.0-s390x-unknown-linux-gnu.tar.gz":       {Linux, OtherArch, Archive},
		"tool-1.0-src.tar.gz":                                 {Any, "", Source},
		"app-win32.zip":                                       {Windows, X86, Archive},
	}
	for name, want := range cases {
		if got := Classify(name); got != want {
			t.Errorf("Classify(%q) = %+v, want %+v", name, got, want)
		}
	}
}

var photocraft = strings.Fields(`photocraft-0.23.1-freebsd-x86_64.tar.gz
photocraft-0.23.1-linux-aarch64.AppImage photocraft-0.23.1-linux-aarch64.AppImage.asc
photocraft-0.23.1-linux-x86_64.AppImage photocraft-0.23.1-linux-x86_64.AppImage.asc
photocraft-0.23.1-linux-x86_64.AppImage.zsync photocraft-0.23.1-linux-x86_64.AppImage.zsync.asc
photocraft-0.23.1-linux-x86_64.deb photocraft-0.23.1-linux-x86_64.deb.asc
photocraft-0.23.1-linux-x86_64.oci.sig photocraft-0.23.1-linux-x86_64.rpm
photocraft-0.23.1-macos-universal.dmg photocraft-0.23.1-windows-x64.msi
photocraft-0.23.1-windows-x64-portable.zip photocraft-0.23.1-windows-x64-symbols.zip
photocraft-0.23.1-windows-arm64.msi photocraft-web-0.23.1.zip
release-signing-key.asc SHA256SUMS.txt SHA256SUMS.txt.asc`)

func chosen(f Filter, names []string) []string {
	var out []string
	for i, ok := range f.Select(names) {
		if ok {
			out = append(out, names[i])
		}
	}
	return out
}

func TestSelectPhotoCraft(t *testing.T) {
	f := Filter{
		OS:      []string{Windows, MacOS, Linux},
		Arch:    []string{X64},
		Formats: []string{"linux/appimage", "windows/msi"},
	}
	got := chosen(f, photocraft)
	want := []string{
		"photocraft-0.23.1-linux-x86_64.AppImage", "photocraft-0.23.1-linux-x86_64.AppImage.asc",
		"photocraft-0.23.1-linux-x86_64.AppImage.zsync", "photocraft-0.23.1-linux-x86_64.AppImage.zsync.asc",
		"photocraft-0.23.1-macos-universal.dmg", "photocraft-0.23.1-windows-x64.msi",
	}
	if !slices.Equal(got, want) {
		t.Errorf("chosen =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// With platform independent files and an extra pattern.
	f.OS = append(f.OS, Any)
	f.Include = []string{"*-portable.zip"}
	got = chosen(f, photocraft)
	for _, n := range []string{"SHA256SUMS.txt", "SHA256SUMS.txt.asc", "release-signing-key.asc", "photocraft-web-0.23.1.zip", "photocraft-0.23.1-windows-x64-portable.zip"} {
		if !slices.Contains(got, n) {
			t.Errorf("%s missing", n)
		}
	}
	if slices.Contains(got, "photocraft-0.23.1-windows-x64-symbols.zip") {
		t.Error("symbols kept")
	}
	// Exclude wins over the rest, Include over Exclude.
	f.Exclude = []string{"*.asc"}
	got = chosen(f, photocraft)
	if slices.Contains(got, "SHA256SUMS.txt.asc") || !slices.Contains(got, "SHA256SUMS.txt") {
		t.Errorf("exclude: %v", got)
	}
}

func TestEmptyKeepsAll(t *testing.T) {
	if got := chosen(Filter{}, photocraft); len(got) != len(photocraft) {
		t.Errorf("%d of %d", len(got), len(photocraft))
	}
}

func TestProblems(t *testing.T) {
	ok := Filter{OS: []string{Linux, Any}, Arch: []string{X64}, Formats: []string{"linux/deb"}, Include: []string{"*.zip"}}
	if p := ok.Problems(); len(p) != 0 {
		t.Errorf("valid filter: %v", p)
	}
	bad := Filter{OS: []string{"plan9"}, Arch: []string{"z80"}, Formats: []string{"linux", "linux/zip"}, Include: []string{"[x"}}
	if p := bad.Problems(); len(p) != 5 {
		t.Errorf("problems = %v", p)
	}
}

func TestDescribe(t *testing.T) {
	o := Describe(photocraft)
	if !slices.Equal(o.OS, []string{Windows, MacOS, Linux, BSD, Any}) {
		t.Errorf("os = %v", o.OS)
	}
	if !slices.Equal(o.Arch, []string{X64, ARM64}) {
		t.Errorf("arch = %v", o.Arch)
	}
	if !slices.Equal(o.Formats[Linux], []string{AppImage, Deb, RPM, Other}) || !slices.Equal(o.Formats[Windows], []string{MSI, Archive, Symbols}) {
		t.Errorf("formats = %v", o.Formats)
	}
}
