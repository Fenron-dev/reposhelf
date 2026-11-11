// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/systrash"
)

// ---- Taking over a local folder (D-032, D-033, T-9.17) ----
//
// A model that is already on the disk does not need to be downloaded
// again. With a Hugging Face ID the model is mirrored from the Hub as
// usual, but the local files are put into its LFS store first, so only
// what is missing is fetched (an LFS object's name is the SHA-256 of its
// content: a file either matches or is ignored). Without an ID the folder
// becomes a local model repository. "Verschieben" puts the original into
// the system trash once every file is verifiably in RepoShelf.

// maxFolderFiles bounds a folder to take over: a model has tens to
// thousands of files; "C:\\" or a home folder is a typing mistake (R-003).
var maxFolderFiles = 20000

// lfsMinSize: smaller files are ordinary git blobs (Hugging Face itself
// stores files from 10 MB in LFS; smaller binaries stay small enough).
const lfsMinSize = 1 << 20

// localModelsOwner is the folder of models that only exist locally.
const localModelsOwner = "modelle"

// skipDirs are never taken over (git data, download caches, macOS files).
var skipDirs = []string{".git", ".cache", ".DS_Store", ".huggingface", "__MACOSX"}

var (
	driveLetter = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	hfCacheDir  = regexp.MustCompile(`^models--([A-Za-z0-9][A-Za-z0-9_.-]*)--([A-Za-z0-9][A-Za-z0-9_.-]*)$`)
	modelIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}/[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)
)

// The system trash; tests replace it so they never touch the real one.
var (
	trashAvailable = systrash.Available
	trashMove      = systrash.Move
)

// IsLocalPath tells a local path (C:\…, \\server\share, /…, ~/…, file://)
// from a link (D-033).
func IsLocalPath(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) ||
		strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "file://") || driveLetter.MatchString(s)
}

// LocalPath turns the input into an absolute, clean path.
func LocalPath(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "file://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		s = u.Path
		if runtime.GOOS == "windows" {
			s = strings.TrimPrefix(s, "/")
		}
	}
	if strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		s = filepath.Join(home, s[2:])
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("%w: kein absoluter Pfad: %s", ErrInvalid, s)
	}
	return filepath.Clean(s), nil
}

// FolderInfo describes a folder offered for taking over.
type FolderInfo struct {
	Path string // what the user gave
	Root string // the folder that is moved to the trash ("Verschieben")
	Dir  string // the files taken over (a snapshot inside the HF cache)
	// ModelID is the Hugging Face ID found in the cache layout.
	ModelID string
	Files   int
	Size    int64
	// CanMove: the system trash can take Root.
	CanMove bool
}

// InspectFolder looks at a local folder without changing anything.
func (l *Library) InspectFolder(input string) (FolderInfo, error) {
	p, err := LocalPath(input)
	if err != nil {
		return FolderInfo{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return FolderInfo{}, fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	if !st.IsDir() {
		return FolderInfo{}, fmt.Errorf("%w: %s ist kein Ordner", ErrInvalid, p)
	}
	if err := l.notDataFolder(p); err != nil {
		return FolderInfo{}, err
	}
	info := FolderInfo{Path: p, Root: p, Dir: p}
	// Hugging Face cache: models--org--name, its snapshots/ or one snapshot.
	for _, dir := range []string{p, filepath.Dir(p), filepath.Dir(filepath.Dir(p))} {
		if m := hfCacheDir.FindStringSubmatch(filepath.Base(dir)); m != nil {
			info.ModelID, info.Root = m[1]+"/"+m[2], dir
			info.Dir = hfSnapshot(dir, p)
			break
		}
	}
	files, err := folderFiles(info.Dir)
	if err != nil {
		return info, err
	}
	for _, f := range files {
		info.Files++
		info.Size += f.size
	}
	if info.Files == 0 {
		return info, fmt.Errorf("%w: %s enthält keine Dateien", ErrInvalid, p)
	}
	info.CanMove = trashAvailable(info.Root)
	return info, nil
}

// notDataFolder refuses folders that are, contain or lie inside the data
// folder: the archive would be copied into itself, and "Verschieben"
// would put it into the trash (R-001).
func (l *Library) notDataFolder(p string) error {
	resolve := func(s string) string {
		if r, err := filepath.EvalSymlinks(s); err == nil {
			return r
		}
		return s
	}
	data, dir := resolve(l.Layout.Root), resolve(p)
	inside := func(child, parent string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && (rel == "." || filepath.IsLocal(rel))
	}
	if inside(dir, data) || inside(data, dir) {
		return fmt.Errorf("%w: %s enthält den RepoShelf-Datenordner oder liegt darin", ErrInvalid, p)
	}
	return nil
}

// hfSnapshot picks the snapshot of a Hugging Face cache folder: the one
// given, else the revision refs/main points to, else the newest.
func hfSnapshot(root, given string) string {
	snaps := filepath.Join(root, "snapshots")
	if strings.HasPrefix(given, snaps+string(filepath.Separator)) {
		rel, _ := filepath.Rel(snaps, given)
		return filepath.Join(snaps, strings.Split(rel, string(filepath.Separator))[0])
	}
	if b, err := os.ReadFile(filepath.Join(root, "refs", "main")); err == nil {
		rev := strings.TrimSpace(string(b))
		if safeSegment(rev) {
			if _, err := os.Stat(filepath.Join(snaps, rev)); err == nil {
				return filepath.Join(snaps, rev)
			}
		}
	}
	entries, _ := os.ReadDir(snaps)
	var best string
	var bestTime int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && e.IsDir() && fi.ModTime().UnixNano() > bestTime {
			best, bestTime = filepath.Join(snaps, e.Name()), fi.ModTime().UnixNano()
		}
	}
	if best == "" {
		return root
	}
	return best
}

func safeSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`)
}

type folderFile struct {
	rel  string // slash separated
	abs  string // resolved (symlinks of the HF cache point into blobs/)
	size int64
	exec bool
}

// folderFiles lists the regular files below dir; symlinks are followed
// (the HF cache links snapshots to blobs) but must stay inside the folder
// tree the user gave.
func folderFiles(dir string) ([]folderFile, error) {
	var out []folderFile
	base := dir
	if hfCacheDir.MatchString(filepath.Base(filepath.Dir(filepath.Dir(dir)))) {
		base = filepath.Dir(filepath.Dir(dir)) // models--…/ incl. blobs/
	}
	if b, err := filepath.EvalSymlinks(base); err == nil {
		base = b // compare resolved with resolved (macOS: /var → /private/var)
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if slices.Contains(skipDirs, d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		abs, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil // broken link: nothing to take over
		}
		if rel, err := filepath.Rel(base, abs); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("%s zeigt aus dem Ordner hinaus", p)
		}
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() {
			return nil
		}
		if len(out) >= maxFolderFiles {
			return fmt.Errorf("%w: mehr als %d Dateien – bitte den Ordner des Modells selbst wählen", ErrInvalid, maxFolderFiles)
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, folderFile{rel: filepath.ToSlash(rel), abs: abs, size: st.Size(), exec: st.Mode().Perm()&0o111 != 0})
		return nil
	})
	return out, err
}

// ImportRequest takes over a local folder.
type ImportRequest struct {
	Path string
	// ModelID links the folder to a Hugging Face model (org/name); empty:
	// the one found in the cache layout, or a local model.
	ModelID string
	// Move puts the original into the system trash after the check.
	Move  bool
	Tags  []string
	Actor string
	Wait  bool
}

// ImportFolder takes over a local folder (D-032, D-033). The repository
// is created at once; copying, checking and moving run in its first sync
// (background job with progress, or right away with Wait).
func (l *Library) ImportFolder(ctx context.Context, req ImportRequest) (*Repo, error) {
	info, err := l.InspectFolder(req.Path)
	if err != nil {
		return nil, err
	}
	if req.Move && !info.CanMove {
		return nil, fmt.Errorf("%w: %s", systrash.ErrUnsupported, info.Root)
	}
	id := strings.TrimSpace(req.ModelID)
	if id == "" {
		id = info.ModelID
	}
	id = strings.TrimSuffix(strings.TrimPrefix(id, "https://huggingface.co/"), "/")
	if id != "" && !modelIDRe.MatchString(id) {
		return nil, fmt.Errorf("%w: Modell-ID %q (erwartet org/name)", ErrInvalid, id)
	}
	state := &ImportState{From: info.Root, Dir: info.Dir, Move: req.Move}
	var r *Repo
	if id != "" {
		r, err = l.Add(ctx, AddRequest{URL: "https://" + sources.HuggingFaceHost + "/" + id, Tags: req.Tags, Actor: req.Actor,
			ConfirmLarge: true, Wait: req.Wait, seed: func(r *Repo) error {
				r.Upstream.Import = state
				return writeUpstream(r.Paths.Meta, r.Upstream)
			}})
	} else {
		r, err = l.CreateLocal(ctx, localModelsOwner, sanitizeName(filepath.Base(info.Root)), "Modell aus "+info.Root, append(req.Tags, "modell"), req.Actor)
		if err == nil {
			r, err = l.Update(ctx, r.R().ID, func(rr *repometa.Repo) error { rr.Include.LFS = true; return nil })
		}
		if err == nil {
			r.Upstream.Import = state
			err = writeUpstream(r.Paths.Meta, r.Upstream)
		}
		if err == nil {
			if req.Wait {
				_, err = l.Sync(ctx, r.R().ID, actorOr(req.Actor))
			} else {
				l.Enqueue(r.R().ID, r.Entry.Key, actorOr(req.Actor))
			}
		}
	}
	if r != nil {
		l.Record(Activity{Actor: actorOr(req.Actor), Action: "import-folder", RepoID: r.R().ID, Detail: info.Root})
		if got, gerr := l.Get(ctx, r.R().ID); gerr == nil {
			r = got
		}
	}
	return r, err
}

// seedImport is the first part of a folder taken over, run by the sync:
// a Hub model gets the local files into its LFS store (the fetch then only
// loads what is missing); a local model becomes one commit.
func (l *Library) seedImport(ctx context.Context, r *Repo) error {
	im := r.Upstream.Import
	files, err := folderFiles(im.Dir)
	if err != nil {
		return err
	}
	if r.R().Source.Host == repometa.LocalHost {
		if err := l.commitFolder(ctx, r, files, im.Move, "Übernommen aus "+im.From); err != nil {
			return err
		}
	} else {
		for i, f := range files {
			l.progress(r.R().ID, fmt.Sprintf("Ordner übernehmen %d/%d", i+1, len(files)), (i*100)/len(files))
			if f.size >= lfsMinSize {
				if _, _, err := gitcli.StoreLFS(r.Paths.Mirror, f.abs, im.Move); err != nil {
					return fmt.Errorf("%s: %w", f.rel, err)
				}
			}
		}
	}
	im.Seeded = true
	return writeUpstream(r.Paths.Meta, r.Upstream)
}

func (l *Library) commitFolder(ctx context.Context, r *Repo, files []folderFile, link bool, message string) error {
	bare := r.Paths.Mirror
	var entries []gitcli.FileEntry
	var lfsPaths []string
	for i, f := range files {
		l.progress(r.R().ID, fmt.Sprintf("Übernehmen %d/%d", i+1, len(files)), (i*100)/len(files))
		if f.size >= lfsMinSize {
			oid, size, err := gitcli.StoreLFS(bare, f.abs, link)
			if err != nil {
				return fmt.Errorf("%s: %w", f.rel, err)
			}
			blob, err := l.Git.HashObject(ctx, bare, strings.NewReader(gitcli.LFSPointer(oid, size)))
			if err != nil {
				return err
			}
			entries = append(entries, gitcli.FileEntry{Path: f.rel, Blob: blob, Exec: f.exec})
			lfsPaths = append(lfsPaths, f.rel)
			continue
		}
		fh, err := os.Open(f.abs)
		if err != nil {
			return err
		}
		blob, err := l.Git.HashObject(ctx, bare, fh)
		fh.Close()
		if err != nil {
			return err
		}
		entries = append(entries, gitcli.FileEntry{Path: f.rel, Blob: blob, Exec: f.exec})
	}
	if len(lfsPaths) > 0 && !slices.ContainsFunc(entries, func(e gitcli.FileEntry) bool { return e.Path == ".gitattributes" }) {
		blob, err := l.Git.HashObject(ctx, bare, strings.NewReader(gitcli.LFSAttributes(lfsPaths)))
		if err != nil {
			return err
		}
		entries = append(entries, gitcli.FileEntry{Path: ".gitattributes", Blob: blob})
	}
	_, err := l.Git.CommitFiles(ctx, bare, "main", message, entries)
	return err
}

// verifyImport counts the files of the folder that are not (byte for
// byte) in the repository: large ones by SHA-256 in the LFS store, small
// ones by git blob ID.
func (l *Library) verifyImport(ctx context.Context, r *Repo, files []folderFile) (int, error) {
	var blobs []string
	missing := 0
	for _, f := range files {
		fh, err := os.Open(f.abs)
		if err != nil {
			return 0, err
		}
		if f.size >= lfsMinSize {
			oid, err := gitcli.SHA256(fh)
			fh.Close()
			if err != nil {
				return 0, err
			}
			if !gitcli.HasLFS(r.Paths.Mirror, oid) {
				missing++
			}
			continue
		}
		id, err := gitcli.BlobID(fh, f.size)
		fh.Close()
		if err != nil {
			return 0, err
		}
		blobs = append(blobs, id)
	}
	if len(blobs) > 0 {
		have, err := l.Git.HasObjects(ctx, r.Paths.Mirror, blobs)
		if err != nil {
			return 0, err
		}
		for _, b := range blobs {
			if !have[b] {
				missing++
			}
		}
	}
	return missing, nil
}

func (l *Library) trashOriginal(r *Repo, root, actor string) error {
	if err := trashMove(root); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "import-moved", RepoID: r.R().ID, Detail: root + " → Papierkorb des Systems"})
	return nil
}

// ImportState is a folder taken over whose check (and move) waits for the
// first sync of the model (upstream.json).
type ImportState struct {
	From string `json:"from"` // folder to move to the trash
	Dir  string `json:"dir"`  // files taken over
	Move bool   `json:"move"`
	// Seeded: the files are in RepoShelf (checking and moving may follow).
	Seeded bool `json:"seeded,omitempty"`
	// Note is the result shown on the repository page.
	Note string `json:"note,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// finishImport runs after a successful sync of a model taken over from a
// folder: check every file, then move the original if asked.
func (l *Library) finishImport(ctx context.Context, r *Repo) {
	im := r.Upstream.Import
	if im == nil || im.Done {
		return
	}
	files, err := folderFiles(im.Dir)
	if err != nil {
		im.Note = "Ordner nicht mehr lesbar: " + err.Error()
		im.Done = true
		return
	}
	missing, err := l.verifyImport(ctx, r, files)
	switch {
	case err != nil:
		im.Note = "Prüfung fehlgeschlagen: " + err.Error()
	case missing > 0:
		im.Note = fmt.Sprintf("%d Dateien des Ordners sind nicht im Modell (z. B. eigene Zusätze) – das Original bleibt liegen.", missing)
	case im.Move:
		if err := l.trashOriginal(r, im.From, "import"); err != nil {
			im.Note = "Original nicht in den Papierkorb gelegt: " + err.Error()
		} else {
			im.Note = "Alle Dateien geprüft übernommen; das Original liegt im Papierkorb des Systems."
		}
	default:
		im.Note = "Alle Dateien geprüft übernommen; das Original ist unverändert."
	}
	im.Done = true
}

// sanitizeName makes a folder name usable as repository name.
func sanitizeName(s string) string {
	if m := hfCacheDir.FindStringSubmatch(s); m != nil {
		s = m[2]
	}
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '-'
	}, s)
	s = strings.Trim(s, "-.")
	if s == "" {
		return "modell"
	}
	return s
}
