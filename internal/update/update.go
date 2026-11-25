// SPDX-License-Identifier: GPL-3.0-or-later

// Package update looks for new RepoShelf releases and installs them where
// that is safe (V-15, T-9.32, D-036).
//
// The check asks the GitHub API for the latest release of RepoShelf, at
// most once a day and only while switched on. Installing depends on how
// RepoShelf runs (Flavor): portable programs (the command-line program, the
// Windows app) download their archive, verify it against the SHA-256 the
// release publishes and replace their own executable, keeping the old one
// next to it; the Mac app downloads the DMG and opens it; in Docker and in
// development builds there is only the hint.
//
// The package depends only on atomicfile, systrash (the previous backup
// goes to the system trash, invariant 2) and the standard library.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/systrash"
)

const (
	// DefaultAPI is the GitHub API; tests use a local server.
	DefaultAPI = "https://api.github.com"
	// Repo is where RepoShelf is released.
	Repo = "Fenron-dev/reposhelf"
	// StateFile keeps the last check (below .reposhelf/).
	StateFile = "update.json"
	// SumsName is the checksum file of a release.
	SumsName = "SHA256SUMS"

	checkEvery    = 24 * time.Hour
	tickEvery     = time.Hour
	metaTimeout   = 30 * time.Second
	maxMetaBytes  = 1 << 20
	maxDownload   = 512 << 20
	maxBinary     = 256 << 20
	backupSuffix  = ".old"
	tempPrefix    = ".reposhelf-update-"
	stateFilePerm = 0o600
	exePerm       = 0o755
)

// Flavor is how this RepoShelf runs, which decides how it updates.
type Flavor string

// Flavors.
const (
	FlavorCLI        Flavor = "cli"         // portable command-line program
	FlavorWindowsApp Flavor = "windows-app" // portable Windows desktop app
	FlavorMacApp     Flavor = "mac-app"     // macOS app from the DMG
	FlavorDocker     Flavor = "docker"
	FlavorDev        Flavor = "dev" // built without a release version
)

// DetectFlavor tells the flavor of the running program. desktop is true in
// the desktop app; inDocker reports a container.
func DetectFlavor(version string, desktop, inDocker bool) Flavor {
	switch {
	case !validVersion(version):
		return FlavorDev
	case inDocker:
		return FlavorDocker
	case desktop && runtime.GOOS == "darwin":
		return FlavorMacApp
	case desktop && runtime.GOOS == "windows":
		return FlavorWindowsApp
	}
	return FlavorCLI
}

// InDocker reports whether the program runs in a container: the image sets
// REPOSHELF_DOCKER, other containers usually have /.dockerenv.
func InDocker(lookupEnv func(string) (string, bool)) bool {
	if v, ok := lookupEnv("REPOSHELF_DOCKER"); ok && v != "" && v != "0" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// Release is a published version.
type Release struct {
	Tag       string    `json:"tag"`
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
	Assets    []Asset   `json:"assets,omitempty"`
}

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// State is what the last check found.
type State struct {
	Checked time.Time `json:"checked"`
	Latest  *Release  `json:"latest,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Notified is the newest version already reported.
	Notified string `json:"notified,omitempty"`
	// Installed is a version installed but not running yet.
	Installed string `json:"installed,omitempty"`
}

// Updater checks and installs. Fields are set once before use.
type Updater struct {
	Current   string // running version (buildinfo.Version)
	Flavor    Flavor
	Exe       string // running executable (replaced by Install)
	StatePath string
	API       string
	Client    *http.Client
	// Enabled reports whether the daily check is switched on.
	Enabled func() bool
	// OnNew is called once per newer version found.
	OnNew func(Release)
	Now   func() time.Time
	// DownloadDir receives the DMG of the Mac app.
	DownloadDir string
	// Open opens a downloaded DMG (macOS: the open command).
	Open func(path string) error
	// GOOS and GOARCH choose the archive; default the running ones.
	GOOS, GOARCH string
	// Trash moves the backup of the previous update away; default the
	// system trash.
	Trash func(path string) error

	mu sync.Mutex
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

// State returns the stored state.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.readState()
}

// Available returns the newer release, if the last check found one.
func (u *Updater) Available() (Release, bool) {
	st := u.State()
	if st.Latest == nil || !Newer(st.Latest.Tag, u.Current) {
		return Release{}, false
	}
	return *st.Latest, true
}

// CanInstall reports whether Install works in this flavor.
func (u *Updater) CanInstall() bool {
	switch u.Flavor {
	case FlavorCLI, FlavorWindowsApp, FlavorMacApp:
		return true
	}
	return false
}

// Run checks once a day while enabled, until ctx ends.
func (u *Updater) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		if u.Enabled == nil || u.Enabled() {
			if st := u.State(); st.Checked.IsZero() || u.now().Sub(st.Checked) >= checkEvery {
				_, _ = u.Check(ctx)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check asks for the latest release and stores the result.
func (u *Updater) Check(ctx context.Context) (State, error) {
	rel, err := u.latest(ctx)
	u.mu.Lock()
	st := u.readState()
	st.Checked = u.now().UTC().Truncate(time.Second)
	st.Error = ""
	var report *Release
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Latest = &rel
		if Newer(rel.Tag, u.Current) && rel.Tag != st.Notified {
			st.Notified = rel.Tag
			report = &rel
		}
	}
	werr := u.writeState(st)
	u.mu.Unlock()
	if report != nil && u.OnNew != nil {
		u.OnNew(*report)
	}
	if err != nil {
		return st, err
	}
	return st, werr
}

func (u *Updater) latest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, metaTimeout)
	defer cancel()
	api := u.API
	if api == "" {
		api = DefaultAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.client().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("update check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("update check: HTTP %d", resp.StatusCode)
	}
	var raw struct {
		Tag       string    `json:"tag_name"`
		URL       string    `json:"html_url"`
		Published time.Time `json:"published_at"`
		Assets    []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxMetaBytes)).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("update check: %w", err)
	}
	if _, ok := parseVersion(raw.Tag); !ok {
		return Release{}, fmt.Errorf("update check: unexpected version %q", raw.Tag)
	}
	rel := Release{Tag: raw.Tag, URL: raw.URL, Published: raw.Published}
	for _, a := range raw.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel, nil
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

// Newer reports whether version a is newer than b (v1.2.3; a version with
// a suffix such as -rc1 is older than the plain one).
func Newer(a, b string) bool {
	va, ok := parseVersion(a)
	if !ok {
		return false
	}
	vb, ok := parseVersion(b)
	if !ok {
		return false
	}
	for i := range 3 {
		if va.n[i] != vb.n[i] {
			return va.n[i] > vb.n[i]
		}
	}
	return va.pre == "" && vb.pre != ""
}

func validVersion(s string) bool {
	_, ok := parseVersion(s)
	return ok
}

type version struct {
	n   [3]int
	pre string
}

// parseVersion reads 1.2.3 or v1.2.3 (release builds carry the version
// without the v, tags have it).
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(s, "v")
	var v version
	s, v.pre, _ = strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.n[i] = n
	}
	return v, true
}

// ---- Installing ----

// ErrNotPossible: this flavor cannot install itself.
var ErrNotPossible = errors.New("update: not possible here")

// Install installs the available release. It returns a message for the
// user (German, shown as is).
func (u *Updater) Install(ctx context.Context) (string, error) {
	rel, ok := u.Available()
	if !ok {
		return "", fmt.Errorf("%w: no newer version known", ErrNotPossible)
	}
	name, inner := u.assetFor(rel.Tag)
	if name == "" {
		return "", ErrNotPossible
	}
	asset, ok := findAsset(rel, name)
	if !ok {
		return "", fmt.Errorf("update: %s is missing in %s", name, rel.Tag)
	}
	want, err := u.checksum(ctx, rel, name)
	if err != nil {
		return "", err
	}
	if u.Flavor == FlavorMacApp {
		return u.installDMG(ctx, rel, asset, want)
	}
	return u.replaceExe(ctx, rel, asset, want, inner)
}

// assetFor names the release file of this flavor and, for archives, the
// executable inside it.
func (u *Updater) assetFor(tag string) (name, inner string) {
	goos, goarch := u.GOOS, u.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	switch u.Flavor {
	case FlavorMacApp:
		return "RepoShelf-macOS.dmg", ""
	case FlavorWindowsApp:
		return "RepoShelf-Windows-portable.zip", "RepoShelf/RepoShelf.exe"
	case FlavorCLI:
		dir := fmt.Sprintf("reposhelf-%s-%s-%s", strings.TrimPrefix(tag, "v"), goos, goarch)
		if goos == "windows" {
			return dir + ".zip", dir + "/reposhelf.exe"
		}
		return dir + ".tar.gz", dir + "/reposhelf"
	}
	return "", ""
}

func findAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// checksum finds the SHA-256 of name in SHA256SUMS or in <name>.sha256.
func (u *Updater) checksum(ctx context.Context, rel Release, name string) (string, error) {
	for _, candidate := range []string{SumsName, name + ".sha256"} {
		a, ok := findAsset(rel, candidate)
		if !ok {
			continue
		}
		body, err := u.fetch(ctx, a.URL, maxMetaBytes)
		if err != nil {
			return "", err
		}
		sc := bufio.NewScanner(strings.NewReader(string(body)))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 0 || len(f[0]) != sha256.Size*2 {
				continue
			}
			if len(f) == 1 && candidate != SumsName || len(f) >= 2 && strings.TrimPrefix(f[1], "*") == name {
				return strings.ToLower(f[0]), nil
			}
		}
	}
	return "", fmt.Errorf("update: no SHA-256 for %s in %s – not installed", name, rel.Tag)
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("update download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update download %s: HTTP %d", path.Base(url), resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// download stores url in a new file in dir and checks its SHA-256.
func (u *Updater) download(ctx context.Context, url, dir, pattern, want string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("update download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update download: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("update download: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
	case n > maxDownload:
		err = errors.New("update download: file too large")
	case hex.EncodeToString(h.Sum(nil)) != want:
		err = errors.New("update download: SHA-256 does not match – not installed")
	}
	if err != nil {
		os.Remove(f.Name()) // our own temporary file
		return "", err
	}
	return f.Name(), nil
}

func (u *Updater) installDMG(ctx context.Context, rel Release, a Asset, want string) (string, error) {
	dir := u.DownloadDir
	if dir == "" {
		return "", fmt.Errorf("%w: no download folder", ErrNotPossible)
	}
	tmp, err := u.download(ctx, a.URL, dir, tempPrefix+"*.dmg", want)
	if err != nil {
		return "", err
	}
	dst := uniquePath(filepath.Join(dir, "RepoShelf-"+rel.Tag+".dmg"))
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("update: %w", err)
	}
	if u.Open != nil {
		if err := u.Open(dst); err != nil {
			return "", fmt.Errorf("update: open %s: %w", dst, err)
		}
	}
	return fmt.Sprintf("%s ist geladen und geprüft (%s). Im geöffneten Fenster RepoShelf in „Programme“ ziehen und danach neu starten.", rel.Tag, dst), nil
}

func (u *Updater) replaceExe(ctx context.Context, rel Release, a Asset, want, inner string) (string, error) {
	if u.Exe == "" {
		return "", fmt.Errorf("%w: program path unknown", ErrNotPossible)
	}
	dir := filepath.Dir(u.Exe)
	archive, err := u.download(ctx, a.URL, dir, tempPrefix+"*"+path.Ext(a.Name), want)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive) // our own temporary file
	newExe, err := extract(archive, inner, dir)
	if err != nil {
		return "", err
	}
	backup := u.Exe + backupSuffix
	if _, err := os.Lstat(backup); err == nil {
		// The backup of the previous update goes to the system trash, not away.
		trash := u.Trash
		if trash == nil {
			trash = systrash.Move
		}
		if err := trash(backup); err != nil {
			os.Remove(newExe)
			return "", fmt.Errorf("update: move old backup %s to the trash: %w", backup, err)
		}
	}
	if err := os.Rename(u.Exe, backup); err != nil {
		os.Remove(newExe)
		return "", fmt.Errorf("update: %w", err)
	}
	if err := os.Rename(newExe, u.Exe); err != nil {
		_ = os.Rename(backup, u.Exe)
		os.Remove(newExe)
		return "", fmt.Errorf("update: %w", err)
	}
	u.mu.Lock()
	st := u.readState()
	st.Installed = rel.Tag
	werr := u.writeState(st)
	u.mu.Unlock()
	return fmt.Sprintf("%s ist installiert und geprüft. Nach einem Neustart von RepoShelf läuft die neue Version; die bisherige liegt als %s daneben.", rel.Tag, filepath.Base(backup)), werr
}

// extract copies the one entry inner of a .zip or .tar.gz into a new file
// in dir. Nothing else of the archive is written.
func extract(archive, inner, dir string) (string, error) {
	var src io.Reader
	var closeFn func() error
	switch {
	case strings.HasSuffix(archive, ".zip"):
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return "", fmt.Errorf("update: %w", err)
		}
		for _, f := range zr.File {
			if f.Name == inner && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					_ = zr.Close()
					return "", fmt.Errorf("update: %w", err)
				}
				src, closeFn = rc, func() error { rc.Close(); return zr.Close() }
				break
			}
		}
		if src == nil {
			_ = zr.Close()
		}
	case strings.HasSuffix(archive, ".gz"):
		f, err := os.Open(archive)
		if err != nil {
			return "", fmt.Errorf("update: %w", err)
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return "", fmt.Errorf("update: %w", err)
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if h.Name == inner && h.Typeflag == tar.TypeReg {
				src, closeFn = tr, f.Close
				break
			}
		}
		if src == nil {
			f.Close()
		}
	}
	if src == nil {
		return "", fmt.Errorf("update: %s not found in the download", inner)
	}
	defer func() { _ = closeFn() }()
	out, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(src, maxBinary+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxBinary {
		err = errors.New("program too large")
	}
	if err == nil {
		err = os.Chmod(out.Name(), exePerm)
	}
	if err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("update: %w", err)
	}
	return out.Name(), nil
}

// uniquePath adds " (2)", " (3)" … before the extension while p exists.
func uniquePath(p string) string {
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}

// ---- State file ----

func (u *Updater) readState() State {
	var st State
	b, err := os.ReadFile(u.StatePath)
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	// After a restart into the installed version it is no longer pending.
	if st.Installed != "" && !Newer(st.Installed, u.Current) {
		st.Installed = ""
	}
	return st
}

// writeState writes the state atomically (invariant 3).
func (u *Updater) writeState(st State) error {
	if u.StatePath == "" {
		return nil
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(u.StatePath, b, stateFilePerm); err != nil {
		return fmt.Errorf("update state: %w", err)
	}
	return nil
}
