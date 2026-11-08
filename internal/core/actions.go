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
	"sort"
	"strconv"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// CI artifacts of GitHub Actions runs are mirrored per run below
// actions/<host>/<owner>/<name>/<run-id>/ with a run.json manifest. Many
// projects publish builds only there; GitHub deletes them after a while.

// ActionRunFile is the manifest of one mirrored run.
const ActionRunFile = "run.json"

// ActionRun is a mirrored CI run.
type ActionRun struct {
	RunID     int64            `json:"run_id"`
	Branch    string           `json:"branch"`
	Commit    string           `json:"commit"`
	Created   time.Time        `json:"created"`
	Artifacts []ActionArtifact `json:"artifacts"`
	Dir       string           `json:"-"`
}

// ActionArtifact is one stored artifact ZIP.
type ActionArtifact struct {
	Name   string `json:"name"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ActionsReport sums up an artifact sync.
type ActionsReport struct {
	Runs, Downloaded int
	Bytes            int64
	Skipped          []string // above the size limit
}

func (l *Library) actionsDir(r *Repo) (string, error) {
	return l.Layout.ActionsDir(r.R().Source)
}

// syncActions downloads artifacts of the newest actions_keep runs.
func (l *Library) syncActions(ctx context.Context, r *Repo) (ActionsReport, error) {
	var rep ActionsReport
	api, err := l.Sources.API(r.R().Source.Host)
	if err != nil {
		return rep, nil
	}
	if l.Sources.Tokens(r.R().Source.Host) == "" {
		return rep, fmt.Errorf("für Actions-Artefakte wird ein Token für %s gebraucht", r.R().Source.HostName())
	}
	arts, err := api.Artifacts(ctx, r.R().Source)
	if errors.Is(err, sources.ErrUnsupported) {
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	keep := l.EffectiveRetention(r).ActionsKeep
	runs := groupRuns(arts)
	if len(runs) > keep {
		runs = runs[:keep]
	}
	base, err := l.actionsDir(r)
	if err != nil {
		return rep, err
	}
	limit := int64(l.Config().Retention.MaxAssetMB) << 20
	for _, run := range runs {
		rep.Runs++
		dir, err := safepath.Join(base, strconv.FormatInt(run.RunID, 10))
		if err != nil {
			return rep, err
		}
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return rep, err
		}
		stored, _ := readActionRun(dir)
		have := map[string]bool{}
		for _, a := range stored.Artifacts {
			have[a.Name] = true
		}
		manifest := ActionRun{RunID: run.RunID, Branch: run.Branch, Commit: run.Commit, Created: run.Created, Artifacts: stored.Artifacts}
		for _, a := range run.arts {
			if have[a.Name] || a.Expired {
				continue
			}
			if limit > 0 && a.Size > limit {
				rep.Skipped = append(rep.Skipped, a.Name)
				continue
			}
			file := encodeName(a.Name) + ".zip"
			h := sha256.New()
			var n int64
			err := atomicfile.Write(filepath.Join(dir, file), filePerm, func(w io.Writer) error {
				cw := &countWriter{w: io.MultiWriter(w, h)}
				err := api.Download(ctx, sources.Asset{Name: a.Name, URL: a.URL}, cw)
				n = cw.n
				return err
			})
			if err != nil {
				return rep, fmt.Errorf("%s: %w", a.Name, err)
			}
			manifest.Artifacts = append(manifest.Artifacts, ActionArtifact{Name: a.Name, File: file, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
			rep.Downloaded++
			rep.Bytes += n
		}
		if err := writeActionRun(dir, manifest); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

type runGroup struct {
	RunID          int64
	Branch, Commit string
	Created        time.Time
	arts           []sources.WorkflowArtifact
}

// groupRuns groups artifacts by run, newest run first; runs whose
// artifacts all expired are left out.
func groupRuns(arts []sources.WorkflowArtifact) []runGroup {
	byRun := map[int64]*runGroup{}
	for _, a := range arts {
		g := byRun[a.RunID]
		if g == nil {
			g = &runGroup{RunID: a.RunID, Branch: a.Branch, Commit: a.Commit, Created: a.Created}
			byRun[a.RunID] = g
		}
		if a.Created.After(g.Created) {
			g.Created = a.Created
		}
		g.arts = append(g.arts, a)
	}
	var out []runGroup
	for _, g := range byRun {
		live := false
		for _, a := range g.arts {
			live = live || !a.Expired
		}
		if live {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func readActionRun(dir string) (ActionRun, error) {
	var run ActionRun
	b, err := os.ReadFile(filepath.Join(dir, ActionRunFile))
	if err != nil {
		return run, err
	}
	err = json.Unmarshal(b, &run)
	run.Dir = dir
	return run, err
}

func writeActionRun(dir string, run ActionRun) error {
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, ActionRunFile), append(b, '\n'), filePerm)
}

// ActionRuns lists mirrored runs of a repository, newest first.
func (l *Library) ActionRuns(ctx context.Context, ref string) ([]ActionRun, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return l.listActionRuns(r), nil
}

func (l *Library) listActionRuns(r *Repo) []ActionRun {
	base, err := l.actionsDir(r)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []ActionRun
	for _, e := range entries {
		if run, err := readActionRun(filepath.Join(base, e.Name())); err == nil && e.IsDir() {
			out = append(out, run)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// ActionArtifactPath returns a stored artifact ZIP for download.
func (l *Library) ActionArtifactPath(ctx context.Context, ref string, runID int64, name string) (string, error) {
	runs, err := l.ActionRuns(ctx, ref)
	if err != nil {
		return "", err
	}
	for _, run := range runs {
		if run.RunID != runID {
			continue
		}
		for _, a := range run.Artifacts {
			if a.Name == name {
				p, err := safepath.Join(run.Dir, a.File)
				if err != nil {
					return "", err
				}
				if _, err := os.Stat(p); err != nil {
					return "", fmt.Errorf("%s: %w", name, fs.ErrNotExist)
				}
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("run %d %s: %w", runID, name, fs.ErrNotExist)
}

// ItemActions are mirrored CI runs beyond actions_keep.
const ItemActions = "actions_run"

func (l *Library) actionCandidates(r *Repo, ret Retention) []CleanupItem {
	var items []CleanupItem
	for i, run := range l.listActionRuns(r) {
		if i < ret.ActionsKeep {
			continue
		}
		var size int64
		for _, a := range run.Artifacts {
			size += a.Size
		}
		items = append(items, CleanupItem{
			ID: itemID(ItemActions, r.R().ID, strconv.FormatInt(run.RunID, 10)), Kind: ItemActions, RepoID: r.R().ID, Key: r.Entry.Key,
			Label:  fmt.Sprintf("Actions-Lauf %d (%s, %d Dateien)", run.RunID, run.Branch, len(run.Artifacts)),
			Reason: fmt.Sprintf("älter als die neuesten %d Läufe", ret.ActionsKeep), Size: size, Since: run.Created, Path: run.Dir,
		})
	}
	return items
}
