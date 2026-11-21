// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- Integrity check (V-1, T-9.21) ----
//
// An archive must notice when its data rots. The check reads everything
// RepoShelf keeps and compares it with what it should be: git objects
// (git fsck), release files and CI artifacts against their stored SHA-256,
// LFS objects, capsule blobs and README images against the SHA-256 in
// their names. It runs on a schedule in the background and on request.

// IntegrityFile keeps the last report (below .reposhelf/).
const IntegrityFile = "integrity.json"

// IntegrityProblem is one damaged or missing item.
type IntegrityProblem struct {
	RepoID string `json:"repo_id,omitempty"`
	Key    string `json:"key,omitempty"`
	Kind   string `json:"kind"` // git, release, artifact, lfs, blob
	Path   string `json:"path"` // below the data folder
	Detail string `json:"detail"`
}

// IntegrityReport is the result of one check.
type IntegrityReport struct {
	Started  time.Time          `json:"started"`
	Finished time.Time          `json:"finished"`
	Repos    int                `json:"repos"`
	Files    int                `json:"files"`
	Bytes    int64              `json:"bytes"`
	Problems []IntegrityProblem `json:"problems"`
	Error    string             `json:"error,omitempty"`
}

// OK reports whether nothing was found.
func (r IntegrityReport) OK() bool { return r.Error == "" && len(r.Problems) == 0 }

// ProblemsOf returns the problems of one repository.
func (r IntegrityReport) ProblemsOf(repoID string) []IntegrityProblem {
	var out []IntegrityProblem
	for _, p := range r.Problems {
		if p.RepoID == repoID {
			out = append(out, p)
		}
	}
	return out
}

var integrityRunning sync.Mutex

// IntegrityRunning reports whether a check is running now.
func (l *Library) IntegrityRunning() bool {
	if integrityRunning.TryLock() {
		integrityRunning.Unlock()
		return false
	}
	return true
}

// ErrCheckRunning is returned when a check is already running.
var ErrCheckRunning = errors.New("eine Integritätsprüfung läuft bereits")

// fsckProblem picks the lines of git fsck that mean data is damaged or
// missing; warnings about odd but intact objects of old repositories
// (time zones, file modes) are not damage.
var (
	fsckProblem = regexp.MustCompile(`(?i)(missing|corrupt|mismatch|broken link|unable to|invalid sha|bad object|error in (tree|commit|tag)|fatal)`)
	fsckBenign  = regexp.MustCompile(`(?i)\b(badTimezone|badDate\w*|zeroPadded\w*|missingSpaceBeforeDate|missingEmail|missingNameBeforeEmail|badEmail|badName|emptyName|multipleAuthors|badFilemode|fullPathname|nulInHeader|extraHeaderEntry|missingTaggerEntry)\b`)
)

// CheckIntegrity reads everything and writes the report (also on error).
func (l *Library) CheckIntegrity(ctx context.Context) (IntegrityReport, error) {
	if !integrityRunning.TryLock() {
		return IntegrityReport{}, ErrCheckRunning
	}
	defer integrityRunning.Unlock()
	rep := IntegrityReport{Started: l.now().UTC().Truncate(time.Second), Problems: []IntegrityProblem{}}
	root := l.Layout.Root
	rel := func(p string) string {
		if r, err := filepath.Rel(root, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	problem := func(e *store.Entry, kind, path, detail string) {
		p := IntegrityProblem{Kind: kind, Path: rel(path), Detail: detail}
		if e != nil {
			p.RepoID, p.Key = e.ID, e.Key
		}
		rep.Problems = append(rep.Problems, p)
	}
	// verify compares a file with the SHA-256 it should have.
	verify := func(e *store.Entry, kind, path, want string) {
		got, n, err := sha256File(path)
		rep.Files++
		rep.Bytes += n
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problem(e, kind, path, "Datei fehlt")
		case err != nil:
			problem(e, kind, path, "nicht lesbar: "+err.Error())
		case !strings.EqualFold(got, want):
			problem(e, kind, path, "Inhalt verändert (Prüfsumme stimmt nicht)")
		}
	}

	entries, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		rep.Error = err.Error()
		return rep, l.writeIntegrity(rep)
	}
	for i := range entries {
		e := &entries[i]
		if ctx.Err() != nil {
			rep.Error = "abgebrochen"
			break
		}
		r, err := l.load(ctx, e.MetaPath)
		if err != nil {
			problem(e, "meta", e.MetaPath, "repo.yaml nicht lesbar: "+err.Error())
			continue
		}
		rep.Repos++
		// Git objects.
		if _, err := os.Stat(r.Paths.Mirror); err == nil {
			out, ferr := l.Git.Run(ctx, r.Paths.Mirror, nil, "fsck", "--no-dangling", "--no-progress", "--full")
			var lines []string
			for _, line := range strings.Split(string(out)+"\n"+gitStderr(ferr), "\n") {
				if line = strings.TrimSpace(line); line != "" && fsckProblem.MatchString(line) && !fsckBenign.MatchString(line) {
					lines = append(lines, line)
				}
			}
			if len(lines) > 0 {
				if len(lines) > 5 {
					lines = append(lines[:5], fmt.Sprintf("… und %d weitere", len(lines)-5))
				}
				problem(e, "git", r.Paths.Mirror, strings.Join(lines, "; "))
			}
			// LFS objects: the name is the content's SHA-256.
			lfs := filepath.Join(r.Paths.Mirror, "lfs", "objects")
			_ = filepath.WalkDir(lfs, func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() && len(d.Name()) == 64 {
					verify(e, "lfs", p, d.Name())
				}
				return nil
			})
		}
		// Release files.
		if rels, err := l.Releases(ctx, e.ID); err == nil {
			for _, rl := range rels {
				for _, a := range rl.Stored {
					if a.SameAs != "" || a.SHA256 == "" {
						continue // checked in the folder that holds the file
					}
					verify(e, "release", filepath.Join(rl.Dir, a.File), a.SHA256)
				}
			}
		}
		// CI artifacts.
		if runs, err := l.ActionRuns(ctx, e.ID); err == nil {
			for _, run := range runs {
				for _, a := range run.Artifacts {
					if a.SHA256 != "" {
						verify(e, "artifact", filepath.Join(run.Dir, a.File), a.SHA256)
					}
				}
			}
		}
		// README images: the file name starts with the content's SHA-256.
		for _, a := range l.ReadmeAssets(r) {
			p := filepath.Join(l.readmeAssetDir(r), a.File)
			got, n, err := sha256File(p)
			rep.Files++
			rep.Bytes += n
			if err != nil || !strings.HasPrefix(got, strings.TrimSuffix(a.File, filepath.Ext(a.File))) {
				problem(e, "readme", p, "Bild fehlt oder ist verändert")
			}
		}
	}
	// Capsule blobs (shared by all repositories).
	blobs := filepath.Join(root, "capsules", "blobs", "sha256")
	_ = filepath.WalkDir(blobs, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && len(d.Name()) == 64 {
			verify(nil, "blob", p, d.Name())
		}
		return nil
	})
	rep.Finished = l.now().UTC().Truncate(time.Second)
	l.Record(Activity{Actor: "integrity", Action: "integrity-check",
		Detail: fmt.Sprintf("%d Repos, %d Dateien geprüft, %d Probleme", rep.Repos, rep.Files, len(rep.Problems))})
	if len(rep.Problems) > 0 {
		l.events.publish(Event{Kind: "integrity", Text: fmt.Sprintf("%d Probleme", len(rep.Problems))})
	}
	return rep, l.writeIntegrity(rep)
}

// gitStderr returns git's error output of a failed call.
func gitStderr(err error) string {
	var ge interface{ Error() string }
	if errors.As(err, &ge) {
		return ge.Error()
	}
	return ""
}

func sha256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (l *Library) integrityPath() string {
	return l.Layout.Path(layout.DirInternal, IntegrityFile)
}

func (l *Library) writeIntegrity(rep IntegrityReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(l.integrityPath(), b, filePerm)
}

// LastIntegrity returns the last report (ok false: none yet).
func (l *Library) LastIntegrity() (IntegrityReport, bool) {
	b, err := os.ReadFile(l.integrityPath())
	if err != nil {
		return IntegrityReport{}, false
	}
	var rep IntegrityReport
	if json.Unmarshal(b, &rep) != nil {
		return IntegrityReport{}, false
	}
	return rep, true
}

// IntegrityDue reports whether the scheduled check should run now.
func (l *Library) IntegrityDue() bool {
	every := time.Duration(l.Config().Retention.IntegrityEvery)
	if every <= 0 {
		return false
	}
	last, ok := l.LastIntegrity()
	return !ok || l.now().Sub(last.Started) >= every
}
