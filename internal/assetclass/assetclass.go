// SPDX-License-Identifier: GPL-3.0-or-later

// Package assetclass tells from the name of a release file which
// operating system, processor and package format it is for, and selects
// files by such a choice (D-035). Projects name their files very
// differently (photocraft-0.23.1-linux-x86_64.AppImage,
// ripgrep-15.2.0-aarch64-apple-darwin.tar.gz, electron-…-win32-x64.zip,
// OllamaSetup.exe), so the rules look at name parts and extensions;
// what is not recognised counts as platform independent and is kept by
// any choice that keeps those. Signatures and checksums of a file
// (.asc, .sig, .sha256, .zsync …) follow that file.
//
// The package has no dependencies inside RepoShelf; labels for the
// interface live in the web package.
package assetclass

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Operating systems. Any is for files that are not tied to one
// (checksum lists, sources, web builds, unknown).
const (
	Windows = "windows"
	MacOS   = "macos"
	Linux   = "linux"
	BSD     = "bsd"
	Android = "android"
	IOS     = "ios"
	Any     = "any"
)

// OSes in display order.
var OSes = []string{Windows, MacOS, Linux, BSD, Android, IOS, Any}

// Processors. Files without one ("Obsidian-1.15.1.exe") match any choice.
const (
	X64       = "x64"
	X86       = "x86"
	ARM64     = "arm64"
	ARM       = "arm"
	RISCV64   = "riscv64"
	Universal = "universal" // macOS builds for both processors; match every choice
	OtherArch = "other"     // s390x, ppc64le, mips, loongarch …
)

// Archs in display order.
var Archs = []string{X64, ARM64, X86, ARM, RISCV64, OtherArch}

// Formats. Symbols (debug symbols, .pdb, dSYM) is a format of its own so
// it can be left out everywhere.
const (
	AppImage = "appimage"
	Deb      = "deb"
	RPM      = "rpm"
	Flatpak  = "flatpak"
	Snap     = "snap"
	ArchPkg  = "archpkg" // .pkg.tar.zst (Arch Linux)
	MSI      = "msi"
	MSIX     = "msix"
	EXE      = "exe"
	DMG      = "dmg"
	PKG      = "pkg"
	APK      = "apk"
	AAB      = "aab"
	IPA      = "ipa"
	Archive  = "archive" // zip, tar.gz … (portable builds)
	Script   = "script"  // install.sh, install.ps1
	Symbols  = "symbols"
	Source   = "source"
	Other    = "other"
)

// Formats in display order.
var Formats = []string{AppImage, Deb, RPM, Flatpak, Snap, ArchPkg, MSI, MSIX, EXE, DMG, PKG, APK, AAB, IPA, Archive, Script, Symbols, Source, Other}

// Class is what a file name says.
type Class struct {
	OS     string
	Arch   string // "" when the name names none
	Format string
}

// Key is the format key used in a Filter ("linux/appimage").
func (c Class) Key() string { return c.OS + "/" + c.Format }

var (
	archRules = []struct {
		re   *regexp.Regexp
		arch string
	}{
		{regexp.MustCompile(`x86[_-]64|amd64|x64|win64|64-?bit`), X64},
		{regexp.MustCompile(`aarch64|arm64`), ARM64},
		{regexp.MustCompile(`riscv64\w*`), RISCV64},
		{regexp.MustCompile(`armv[5-7]\w*|armhf|armel|\barm\b|gnueabi\w*`), ARM},
		{regexp.MustCompile(`i[3-6]86|\bx86\b|\b386\b|32-?bit`), X86},
		{regexp.MustCompile(`universal2?`), Universal},
		{regexp.MustCompile(`s390x|ppc64\w*|mips\w*|loong\w*|sparc\w*`), OtherArch},
	}
	// sep splits names into parts; "_" and "-" separate, "." too.
	sep = regexp.MustCompile(`[-_.\s+]+`)
)

// osWords maps name parts to an operating system.
var osWords = map[string]string{
	"windows": Windows, "win": Windows, "win32": Windows, "win64": Windows, "msvc": Windows, "mingw": Windows, "mingw32": Windows, "mingw64": Windows,
	"macos": MacOS, "mac": MacOS, "osx": MacOS, "darwin": MacOS, "apple": MacOS, "mas": MacOS, "macosx": MacOS,
	"linux": Linux, "ubuntu": Linux, "debian": Linux, "fedora": Linux, "centos": Linux, "rhel": Linux, "opensuse": Linux, "suse": Linux, "alpine": Linux, "manylinux": Linux, "musllinux": Linux, "el8": Linux, "el9": Linux,
	"freebsd": BSD, "openbsd": BSD, "netbsd": BSD, "dragonfly": BSD,
	"android": Android, "ios": IOS, "iphoneos": IOS,
}

// symbolWords mark debug symbols.
var symbolWords = map[string]bool{"symbols": true, "dsym": true, "pdb": true, "debug": true, "dbg": true, "debuginfo": true, "debugsymbols": true}

// sourceWords mark source archives.
var sourceWords = map[string]bool{"src": true, "source": true, "sources": true, "vendor": true, "vendored": true}

// extFormats in order: longer extensions first. os is the system the
// extension implies ("" = none).
var extFormats = []struct{ ext, format, os string }{
	{".pkg.tar.zst", ArchPkg, Linux}, {".pkg.tar.xz", ArchPkg, Linux}, {".pkg.tar.gz", ArchPkg, Linux},
	{".appimage", AppImage, Linux}, {".deb", Deb, Linux}, {".rpm", RPM, Linux},
	{".flatpak", Flatpak, Linux}, {".flatpakref", Flatpak, Linux}, {".snap", Snap, Linux},
	{".msi", MSI, Windows}, {".msix", MSIX, Windows}, {".msixbundle", MSIX, Windows}, {".appx", MSIX, Windows}, {".appxbundle", MSIX, Windows},
	{".exe", EXE, Windows}, {".ps1", Script, Windows}, {".bat", Script, Windows}, {".cmd", Script, Windows},
	{".dmg", DMG, MacOS}, {".pkg", PKG, MacOS},
	{".apk", APK, Android}, {".aab", AAB, Android}, {".ipa", IPA, IOS},
	{".sh", Script, ""},
	{".tar.gz", Archive, ""}, {".tgz", Archive, ""}, {".tar.xz", Archive, ""}, {".txz", Archive, ""},
	{".tar.zst", Archive, ""}, {".tar.bz2", Archive, ""}, {".tbz2", Archive, ""}, {".zip", Archive, ""},
	{".7z", Archive, ""}, {".gz", Archive, ""}, {".xz", Archive, ""}, {".zst", Archive, ""}, {".bz2", Archive, ""}, {".tar", Archive, ""},
}

// companionExts are files that belong to another file of the release.
var companionExts = []string{".asc", ".sig", ".minisig", ".sha1", ".sha256", ".sha512", ".sha256sum", ".sha512sum", ".md5", ".zsync", ".blockmap", ".pem", ".cert", ".crt", ".sbom", ".intoto.jsonl", ".bundle", ".sigstore"}

// Classify reads a file name.
func Classify(name string) Class {
	lower := strings.ToLower(path.Base(name))
	var c Class
	extOS := ""
	for _, e := range extFormats {
		if strings.HasSuffix(lower, e.ext) {
			c.Format, extOS = e.format, e.os
			break
		}
	}
	if c.Format == "" {
		c.Format = Other
	}
	for _, r := range archRules {
		if r.re.MatchString(lower) {
			c.Arch = r.arch
			break
		}
	}
	parts := sep.Split(lower, -1)
	for _, p := range parts {
		if os, ok := osWords[p]; ok && os != "" && c.OS == "" {
			c.OS = os
		}
	}
	if c.OS == "" && strings.Contains(lower, "windows") {
		c.OS = Windows
	}
	if c.OS == "" {
		c.OS = extOS
	}
	// win32 names the system (electron: win32-x64); only without another
	// processor it means 32 bit.
	if c.Arch == "" && slices.Contains(parts, "win32") {
		c.Arch = X86
	}
	for _, p := range parts {
		switch {
		case symbolWords[p]:
			c.Format = Symbols
		case sourceWords[p] && c.Format != Symbols:
			c.Format, c.OS, c.Arch = Source, Any, ""
		}
	}
	if c.OS == "" {
		c.OS = Any
	}
	return c
}

// Filter is a choice of files. Empty lists mean "all".
type Filter struct {
	OS []string
	// Arch: files without a processor in their name, and universal macOS
	// builds, match every choice.
	Arch []string
	// Formats as "os/format" ("linux/appimage"); an operating system
	// without any entry keeps all its formats.
	Formats []string
	// Include and Exclude are name patterns (path.Match, case-insensitive);
	// Include wins over everything, Exclude over the rest.
	Include []string
	Exclude []string
}

// Empty reports whether the filter keeps everything.
func (f Filter) Empty() bool {
	return len(f.OS) == 0 && len(f.Arch) == 0 && len(f.Formats) == 0 && len(f.Include) == 0 && len(f.Exclude) == 0
}

// Problems lists invalid entries (unknown keys, broken patterns).
func (f Filter) Problems() []string {
	var out []string
	for _, o := range f.OS {
		if !slices.Contains(OSes, o) {
			out = append(out, fmt.Sprintf("unknown os %q (allowed: %s)", o, strings.Join(OSes, ", ")))
		}
	}
	for _, a := range f.Arch {
		if !slices.Contains(Archs, a) {
			out = append(out, fmt.Sprintf("unknown arch %q (allowed: %s)", a, strings.Join(Archs, ", ")))
		}
	}
	for _, k := range f.Formats {
		o, fm, ok := strings.Cut(k, "/")
		if !ok || !slices.Contains(OSes, o) || !slices.Contains(Formats, fm) {
			out = append(out, fmt.Sprintf("unknown format %q (os/format, e.g. linux/appimage)", k))
		}
	}
	for _, p := range append(slices.Clone(f.Include), f.Exclude...) {
		if _, err := path.Match(strings.ToLower(p), ""); err != nil || p == "" || len(p) > 200 {
			out = append(out, fmt.Sprintf("invalid pattern %q", p))
		}
	}
	return out
}

// Keep reports whether a file with this class and name is chosen,
// without looking at companions (see Select).
func (f Filter) Keep(name string) bool {
	lower := strings.ToLower(path.Base(name))
	if matchAny(f.Include, lower) {
		return true
	}
	if matchAny(f.Exclude, lower) {
		return false
	}
	c := Classify(name)
	if len(f.OS) > 0 && !slices.Contains(f.OS, c.OS) {
		return false
	}
	if len(f.Arch) > 0 && c.Arch != "" && c.Arch != Universal && !slices.Contains(f.Arch, c.Arch) {
		return false
	}
	prefix := c.OS + "/"
	limited := slices.ContainsFunc(f.Formats, func(k string) bool { return strings.HasPrefix(k, prefix) })
	return !limited || slices.Contains(f.Formats, c.Key())
}

func matchAny(patterns []string, lower string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), lower); ok {
			return true
		}
	}
	return false
}

// MainFile returns the file a companion belongs to ("x.AppImage" for
// "x.AppImage.zsync.asc") if that file is among names; ok is false for
// files of their own.
func MainFile(name string, names map[string]bool) (string, bool) {
	cur := name
	for {
		lower := strings.ToLower(cur)
		cut := ""
		for _, e := range companionExts {
			if strings.HasSuffix(lower, e) && len(cur) > len(e) {
				cut = cur[:len(cur)-len(e)]
				break
			}
		}
		if cut == "" {
			return "", false
		}
		if names[cut] {
			// The main file may itself be a companion (.zsync of an AppImage).
			if m, ok := MainFile(cut, names); ok {
				return m, true
			}
			return cut, true
		}
		cur = cut
	}
}

// Select reports for each name of one release whether it is chosen.
// Companions follow their main file unless a pattern names them.
func (f Filter) Select(names []string) []bool {
	out := make([]bool, len(names))
	if f.Empty() {
		for i := range out {
			out[i] = true
		}
		return out
	}
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	for i, n := range names {
		lower := strings.ToLower(n)
		switch {
		case matchAny(f.Include, lower):
			out[i] = true
		case matchAny(f.Exclude, lower):
			out[i] = false
		default:
			if m, ok := MainFile(n, set); ok {
				out[i] = f.Keep(m)
			} else {
				out[i] = f.Keep(n)
			}
		}
	}
	return out
}

// Offer summarises what releases contain: which systems, processors and
// formats occur, so a choice offers only those.
type Offer struct {
	OS      []string            // in OSes order
	Arch    []string            // in Archs order
	Formats map[string][]string // os → formats in Formats order
}

// Describe builds the offer of the given file names (all releases).
func Describe(names []string) Offer {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	oss, archs, fmts := map[string]bool{}, map[string]bool{}, map[string]map[string]bool{}
	for _, n := range names {
		if _, ok := MainFile(n, set); ok {
			continue
		}
		c := Classify(n)
		oss[c.OS] = true
		if c.Arch != "" && c.Arch != Universal {
			archs[c.Arch] = true
		}
		if fmts[c.OS] == nil {
			fmts[c.OS] = map[string]bool{}
		}
		fmts[c.OS][c.Format] = true
	}
	o := Offer{Formats: map[string][]string{}}
	for _, x := range OSes {
		if oss[x] {
			o.OS = append(o.OS, x)
			for _, fm := range Formats {
				if fmts[x][fm] {
					o.Formats[x] = append(o.Formats[x], fm)
				}
			}
		}
	}
	for _, a := range Archs {
		if archs[a] {
			o.Arch = append(o.Arch, a)
		}
	}
	return o
}
