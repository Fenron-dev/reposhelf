// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/fsinfo"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Check is one doctor result (German texts: shown to the user as is).
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// DoctorReport collects checks.
type DoctorReport struct{ Checks []Check }

// OK reports whether every check passed.
func (d DoctorReport) OK() bool {
	for _, c := range d.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// Doctor checks the installation and the data folder and repairs what is
// safe to repair (stale temporary files, T-0.13).
func (l *Library) Doctor(ctx context.Context) DoctorReport {
	var rep DoctorReport
	add := func(name string, ok bool, format string, args ...any) {
		rep.Checks = append(rep.Checks, Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, args...)})
	}
	if v, err := l.Git.Check(ctx); err != nil {
		add("Git", false, "%v", err)
	} else {
		add("Git", true, "%s (%s)", v, l.Git.Path)
	}
	if l.lfs {
		add("Git LFS", true, "installiert")
	} else {
		add("Git LFS", true, "nicht installiert – große Dateien (LFS) werden nicht gesichert")
	}
	fsType := fsinfo.Type(l.Layout.Root)
	add("Datenordner", true, "%s (Dateisystem %s, Index im Modus %s)", l.Layout.Root, orUnknown(fsType), l.Store.Journal())
	probe := l.Layout.Path(".reposhelf", ".write-test")
	if err := os.WriteFile(probe, []byte("x"), filePerm); err != nil {
		add("Schreibrecht", false, "%v", err)
	} else {
		os.Remove(probe)
		add("Schreibrecht", true, "vorhanden")
	}
	if removed, err := l.Layout.CleanTemp(l.now()); err != nil {
		add("Zwischendateien", false, "%v", err)
	} else {
		add("Zwischendateien", true, "%d liegengebliebene entfernt", len(removed))
	}
	for _, w := range l.warnings {
		add("Konfiguration", true, "Warnung: %s", w)
	}
	files, err := l.Layout.RepoFiles()
	if err != nil {
		add("Metadaten", false, "%v", err)
	} else {
		entries, _ := l.Store.List(ctx, store.Query{})
		ok := len(entries) == len(files)
		add("Index", ok, "%d Repos im Index, %d repo.yaml-Dateien%s", len(entries), len(files), map[bool]string{false: " – „reposhelf reindex“ ausführen", true: ""}[ok])
		var broken []string
		for _, e := range entries {
			if !l.Git.HasCommits(ctx, mirrorOf(l, e)) && e.LastSuccess != nil {
				broken = append(broken, e.Key)
			}
		}
		add("Spiegel", len(broken) == 0, "%s", map[bool]string{true: "alle lesbar", false: "ohne Inhalt: " + strings.Join(broken, ", ")}[len(broken) == 0])
	}
	if tok := l.Sources.Tokens("github.com"); tok == "" {
		add("GitHub-Token", true, "keines gesetzt – 60 statt 5000 API-Anfragen pro Stunde (REPOSHELF_GITHUB_TOKEN)")
	} else {
		add("GitHub-Token", true, "gesetzt")
	}
	return rep
}

func mirrorOf(l *Library, e store.Entry) string {
	r, err := l.load(context.Background(), e.MetaPath)
	if err != nil {
		return ""
	}
	return r.Paths.Mirror
}

func orUnknown(s string) string {
	if s == "" {
		return "unbekannt"
	}
	return s
}
