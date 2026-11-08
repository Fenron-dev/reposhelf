// SPDX-License-Identifier: GPL-3.0-or-later

// Package artifact defines how builds and build capsules are stored in the
// data folder, so that the core (retention, trash) and the runner
// (execution) agree on one format:
//
//	builds/<host>/<owner>/<name>/<build-id>/build.yaml, log.txt, artifacts/
//	capsules/<host>/<owner>/<name>/<version>.yaml        capsule manifest
//	capsules/<host>/<owner>/<name>/<version>.files.json  file list
//	capsules/blobs/sha256/<ab>/<hash>                    shared, deduplicated files
//
// Everything is plain files (YAML/JSON), readable without RepoShelf.
// It depends on atomicfile, safepath and repometa.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

// Build states.
const (
	StatusWaiting   = "waiting_confirmation"
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Build kinds.
const (
	KindDocker      = "docker"       // reposhelf-build.yaml target in a container
	KindAct         = "act"          // GitHub workflow run locally with act
	KindGitHub      = "github"       // GitHub Actions as external runner
	KindCapsule     = "capsule"      // create a build capsule
	KindCapsuleTest = "capsule_test" // offline build from a capsule
)

const (
	BuildFile    = "build.yaml"
	LogFile      = "log.txt"
	ArtifactsDir = "artifacts"
	filePerm     = 0o644
	dirPerm      = 0o755
)

// Build is one build job (build.yaml).
type Build struct {
	ID        string     `yaml:"id"`
	RepoID    string     `yaml:"repo_id"`
	Key       string     `yaml:"key"`
	Ref       string     `yaml:"ref"`
	Commit    string     `yaml:"commit"`
	Target    string     `yaml:"target"`
	Kind      string     `yaml:"kind"`
	Runner    string     `yaml:"runner"`
	Status    string     `yaml:"status"`
	Actor     string     `yaml:"actor"`
	Created   time.Time  `yaml:"created"`
	Started   *time.Time `yaml:"started"`
	Finished  *time.Time `yaml:"finished"`
	Error     string     `yaml:"error"`
	External  string     `yaml:"external_url"`
	Artifacts []File     `yaml:"artifacts"`
	// Version is set for capsule builds.
	Version string `yaml:"version"`
	Dir     string `yaml:"-"`
}

// File is a stored file with checksum.
type File struct {
	Name   string `yaml:"name" json:"path"`
	Size   int64  `yaml:"size" json:"size"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Mode   uint32 `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// Done reports whether the build has finished.
func (b Build) Done() bool {
	return b.Status == StatusSuccess || b.Status == StatusFailed || b.Status == StatusCancelled
}

// Duration of the build so far.
func (b Build) Duration(now time.Time) time.Duration {
	if b.Started == nil {
		return 0
	}
	end := now
	if b.Finished != nil {
		end = *b.Finished
	}
	return end.Sub(*b.Started).Round(time.Second)
}

// BuildsDir is builds/<segments…>.
func BuildsDir(root string, src repometa.Source) (string, error) {
	return safepath.Join(filepath.Join(root, "builds"), src.Segments()...)
}

// WriteBuild stores build.yaml atomically.
func WriteBuild(b Build) error {
	if err := os.MkdirAll(b.Dir, dirPerm); err != nil {
		return err
	}
	out, err := repometa.NewFile(b).Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(b.Dir, BuildFile), out, filePerm)
}

// ReadBuild loads build.yaml from dir.
func ReadBuild(dir string) (Build, error) {
	raw, err := os.ReadFile(filepath.Join(dir, BuildFile))
	if err != nil {
		return Build{}, err
	}
	f, err := repometa.ParseFile(raw, Build{})
	if err != nil {
		return Build{}, err
	}
	b := f.Value
	b.Dir = dir
	return b, nil
}

// ListBuilds walks builds/ and returns all builds, newest first.
func ListBuilds(root string) []Build {
	var out []Build
	base := filepath.Join(root, "builds")
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == BuildFile {
			if b, err := ReadBuild(filepath.Dir(p)); err == nil {
				out = append(out, b)
			}
		}
		if d.IsDir() && (d.Name() == ArtifactsDir || d.Name() == "work") {
			return fs.SkipDir
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// ---- Capsules (KONZEPT 6.11) ----

// Ecosystem is one dependency ecosystem inside a capsule.
type Ecosystem struct {
	Name      string `yaml:"name"`      // go, rust, dart, flutter, node, python, docker
	Toolchain string `yaml:"toolchain"` // e.g. "go 1.24.2", "rust 1.85.0"
	Image     string `yaml:"image"`     // container image used
	Fetch     string `yaml:"fetch"`     // command that fetched dependencies
	Build     string `yaml:"build"`     // offline build command (capsule test)
	Files     int    `yaml:"files"`
	Size      int64  `yaml:"size"`
}

// Capsule is the manifest of one capsule (<version>.yaml).
type Capsule struct {
	Schema     int         `yaml:"schema"`
	RepoID     string      `yaml:"repo_id"`
	Key        string      `yaml:"key"`
	Version    string      `yaml:"version"`
	Commit     string      `yaml:"commit"`
	Created    time.Time   `yaml:"created"`
	BuildID    string      `yaml:"build_id"`
	Ecosystems []Ecosystem `yaml:"ecosystems"`
	Size       int64       `yaml:"size"`
	FileCount  int         `yaml:"file_count"`
	Test       *TestResult `yaml:"test"`
	Path       string      `yaml:"-"`
}

// TestResult is the outcome of the last offline build (T-6.6).
type TestResult struct {
	Status  string    `yaml:"status"`
	Date    time.Time `yaml:"date"`
	BuildID string    `yaml:"build_id"`
}

// CapsuleDir is capsules/<segments…>.
func CapsuleDir(root string, src repometa.Source) (string, error) {
	return safepath.Join(filepath.Join(root, "capsules"), src.Segments()...)
}

// encodeVersion makes a version usable as a file name (tags may contain "/").
func encodeVersion(v string) string {
	s := strings.NewReplacer("/", "%2F", "\\", "%5C", ":", "%3A").Replace(v)
	if safepath.CheckSegment(s+".yaml") != nil {
		s = "_" + hex.EncodeToString([]byte(v))
	}
	return s
}

// CapsulePaths returns manifest and file-list paths of a version.
func CapsulePaths(dir, version string) (manifest, files string) {
	base := filepath.Join(dir, encodeVersion(version))
	return base + ".yaml", base + ".files.json"
}

// WriteCapsule stores manifest and file list.
func WriteCapsule(dir string, c Capsule, files []File) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	m, f := CapsulePaths(dir, c.Version)
	c.Schema = 1
	out, err := repometa.NewFile(c).Marshal()
	if err != nil {
		return err
	}
	if files != nil {
		b, err := json.Marshal(files)
		if err != nil {
			return err
		}
		if err := atomicfile.WriteFile(f, b, filePerm); err != nil {
			return err
		}
	}
	return atomicfile.WriteFile(m, out, filePerm)
}

// ReadCapsules lists the capsules in dir, newest first.
func ReadCapsules(dir string) []Capsule {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Capsule
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		f, err := repometa.ParseFile(raw, Capsule{})
		if err != nil {
			continue
		}
		c := f.Value
		c.Path = p
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// ReadFiles loads the file list next to a manifest.
func ReadFiles(manifest string) ([]File, error) {
	b, err := os.ReadFile(strings.TrimSuffix(manifest, ".yaml") + ".files.json")
	if err != nil {
		return nil, err
	}
	var files []File
	return files, json.Unmarshal(b, &files)
}

// Blobs is the content-addressed store shared by all capsules (T-6.5): a
// package used by ten projects is stored once.
type Blobs struct{ Root string } // <data>/capsules/blobs

// BlobsOf returns the store of a data folder.
func BlobsOf(dataRoot string) Blobs { return Blobs{Root: filepath.Join(dataRoot, "capsules", "blobs")} }

// Path of a blob.
func (b Blobs) Path(sum string) string {
	if len(sum) < 2 {
		return filepath.Join(b.Root, "sha256", "_", sum)
	}
	return filepath.Join(b.Root, "sha256", sum[:2], sum)
}

// Put stores the file at src (if not yet stored) and returns its entry.
func (b Blobs) Put(src, name string) (File, bool, error) {
	f, err := os.Open(src)
	if err != nil {
		return File{}, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return File{}, false, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return File{}, false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	entry := File{Name: name, Size: info.Size(), SHA256: sum, Mode: uint32(info.Mode().Perm())}
	dst := b.Path(sum)
	if _, err := os.Stat(dst); err == nil {
		return entry, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return entry, false, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return entry, false, err
	}
	err = atomicfile.Write(dst, 0o444, func(w io.Writer) error {
		_, err := io.Copy(w, f)
		return err
	})
	return entry, err == nil, err
}

// Materialize writes the files of a capsule below dir (capsule test).
func (b Blobs) Materialize(files []File, dir string) error {
	for _, f := range files {
		rel := filepath.FromSlash(f.Name)
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("unsafe path in capsule: %q", f.Name)
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
			return err
		}
		in, err := os.Open(b.Path(f.SHA256))
		if err != nil {
			return fmt.Errorf("blob %s missing: %w", f.SHA256, err)
		}
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = filePerm
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o200)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Referenced returns all blob hashes used by capsules below capsulesRoot.
func Referenced(capsulesRoot string) map[string]bool {
	used := map[string]bool{}
	_ = filepath.WalkDir(capsulesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "blobs" {
			return fs.SkipDir
		}
		if strings.HasSuffix(p, ".files.json") {
			files, err := ReadFiles(strings.TrimSuffix(p, ".files.json") + ".yaml")
			if err == nil {
				for _, f := range files {
					used[f.SHA256] = true
				}
			}
		}
		return nil
	})
	return used
}

// Unused lists blobs no capsule references (for the cleanup dialog).
func (b Blobs) Unused(used map[string]bool) ([]string, int64) {
	var list []string
	var size int64
	_ = filepath.WalkDir(filepath.Join(b.Root, "sha256"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !used[d.Name()] && !atomicfile.IsTemp(d.Name()) {
			list = append(list, p)
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return list, size
}

// ErrNoCapsule is returned when a version has no capsule.
var ErrNoCapsule = errors.New("no capsule for this version")
