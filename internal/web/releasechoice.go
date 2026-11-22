// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/assetclass"
	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ---- Choice of release files (D-035, T-9.26–T-9.28) ----
//
// One form block serves the add dialog, the release settings of a
// repository and the profile editor. Checkboxes offer only what the
// releases contain (all values in the profile editor). The block is
// preselected from a profile; whether the result is still that profile or
// a choice of its own is decided by what it selects, not by which box was
// touched, so ticking a box back makes it the profile again.

// Form field names of the block.
const (
	fProfile = "rel_profile"
	fOS      = "rel_os"
	fArch    = "rel_arch"
	fFormat  = "rel_format"
	fInclude = "rel_include"
	fExclude = "rel_exclude"
	fKeep    = "rel_keep"
	fPin     = "rel_pin"
	// fShown marks that the block was part of the form (a repository
	// without releases has none).
	fShown = "rel_shown"
	// ownProfile in the profile list means "a choice of its own".
	ownProfile = "-"
)

// releaseForm is the view of the block.
type releaseForm struct {
	// Mode is add, repo or profile (which endpoint refreshes the block).
	Mode     string
	Action   string // refresh endpoint
	Offer    assetclass.Offer
	Profiles []string
	Default  string // name of the default profile
	Profile  string // selected: "" = default, ownProfile, or a name
	Own      bool   // the boxes differ from the profile
	OS       map[string]bool
	Arch     map[string]bool
	Format   map[string]bool // os/format
	Include  string
	Exclude  string
	Keep     int
	// KeepFrom names where the preselected number comes from.
	KeepFrom string
	Releases []sources.Release
	Pinned   map[string]bool
	Estimate *core.ReleaseEstimate
}

// fullOffer is every value, for the profile editor.
func fullOffer() assetclass.Offer {
	o := assetclass.Offer{OS: assetclass.OSes, Arch: assetclass.Archs, Formats: map[string][]string{}}
	for _, os := range assetclass.OSes {
		o.Formats[os] = osFormats(os)
	}
	return o
}

// osFormats are the formats worth offering per system in the editor.
func osFormats(os string) []string {
	common := []string{assetclass.Archive, assetclass.Symbols, assetclass.Other}
	switch os {
	case assetclass.Windows:
		return append([]string{assetclass.MSI, assetclass.MSIX, assetclass.EXE, assetclass.Script}, common...)
	case assetclass.MacOS:
		return append([]string{assetclass.DMG, assetclass.PKG}, common...)
	case assetclass.Linux:
		return append([]string{assetclass.AppImage, assetclass.Deb, assetclass.RPM, assetclass.Flatpak, assetclass.Snap, assetclass.ArchPkg}, common...)
	case assetclass.Android:
		return []string{assetclass.APK, assetclass.AAB, assetclass.Other}
	case assetclass.IOS:
		return []string{assetclass.IPA, assetclass.Other}
	case assetclass.Any:
		return []string{assetclass.Archive, assetclass.Script, assetclass.Source, assetclass.Other}
	}
	return common
}

// preselect ticks the boxes a filter keeps.
func (rf *releaseForm) preselect(f assetclass.Filter) {
	rf.OS, rf.Arch, rf.Format = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, os := range rf.Offer.OS {
		rf.OS[os] = len(f.OS) == 0 || slices.Contains(f.OS, os)
		limited := slices.ContainsFunc(f.Formats, func(k string) bool { return strings.HasPrefix(k, os+"/") })
		for _, fm := range rf.Offer.Formats[os] {
			rf.Format[os+"/"+fm] = !limited || slices.Contains(f.Formats, os+"/"+fm)
		}
	}
	for _, a := range rf.Offer.Arch {
		rf.Arch[a] = len(f.Arch) == 0 || slices.Contains(f.Arch, a)
	}
	rf.Include, rf.Exclude = strings.Join(f.Include, ", "), strings.Join(f.Exclude, ", ")
}

// fromForm reads the boxes.
func (rf *releaseForm) fromForm(f url.Values) {
	rf.OS, rf.Arch, rf.Format = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range f[fOS] {
		rf.OS[v] = true
	}
	for _, v := range f[fArch] {
		rf.Arch[v] = true
	}
	for _, v := range f[fFormat] {
		rf.Format[v] = true
	}
	rf.Include, rf.Exclude = f.Get(fInclude), f.Get(fExclude)
}

// filter turns the boxes into a filter: a list that ticks everything on
// offer stays empty ("all", also for systems a later release adds).
func (rf *releaseForm) filter() assetclass.Filter {
	var f assetclass.Filter
	allOS := true
	for _, os := range rf.Offer.OS {
		var fmts []string
		allFmt := true
		for _, fm := range rf.Offer.Formats[os] {
			if rf.Format[os+"/"+fm] {
				fmts = append(fmts, os+"/"+fm)
			} else {
				allFmt = false
			}
		}
		if !rf.OS[os] || len(fmts) == 0 {
			allOS = false
			continue
		}
		f.OS = append(f.OS, os)
		if !allFmt {
			f.Formats = append(f.Formats, fmts...)
		}
	}
	if allOS {
		f.OS = nil
	}
	allArch := true
	for _, a := range rf.Offer.Arch {
		if rf.Arch[a] {
			f.Arch = append(f.Arch, a)
		} else {
			allArch = false
		}
	}
	if allArch {
		f.Arch = nil
	}
	f.Include, f.Exclude = splitList(rf.Include), splitList(rf.Exclude)
	return f
}

// profileName is the profile the selection refers to.
func (rf *releaseForm) profileName() string {
	if rf.Profile == "" || rf.Profile == ownProfile {
		return rf.Default
	}
	return rf.Profile
}

// sameSelection reports whether two filters choose the same files of the
// known releases (patterns aside, which are stored with the profile).
func sameSelection(a, b assetclass.Filter, rels []sources.Release) bool {
	a.Include, a.Exclude, b.Include, b.Exclude = nil, nil, nil, nil
	for _, r := range rels {
		names := make([]string, len(r.Assets))
		for i, as := range r.Assets {
			names[i] = as.Name
		}
		if !slices.Equal(a.Select(names), b.Select(names)) {
			return false
		}
	}
	return true
}

// files is the choice for repo.yaml: nil (default profile), a profile
// with extra patterns, or a choice of its own.
func (rf *releaseForm) files(cfg config.Config) *repometa.ReleaseFiles {
	own := rf.filter()
	prof, _ := cfg.Profile(rf.profileName())
	rf.Own = rf.Profile == ownProfile || !sameSelection(own, prof.Filter(), rf.Releases)
	if rf.Own {
		return &repometa.ReleaseFiles{OS: own.OS, Arch: own.Arch, Formats: own.Formats, Include: own.Include, Exclude: own.Exclude}
	}
	// Patterns of the profile itself are not repeated in repo.yaml.
	inc := slices.DeleteFunc(own.Include, func(p string) bool { return slices.Contains(prof.Include, p) })
	exc := slices.DeleteFunc(own.Exclude, func(p string) bool { return slices.Contains(prof.Exclude, p) })
	if rf.Profile == "" && len(inc) == 0 && len(exc) == 0 {
		return nil
	}
	return &repometa.ReleaseFiles{Profile: rf.Profile, Include: inc, Exclude: exc}
}

// keepOf is the number of releases a profile keeps (or the global one).
func keepOf(cfg config.Config, name string) (int, string) {
	if p, ok := cfg.Profile(name); ok && p.ReleasesKeep != nil {
		return *p.ReleasesKeep, "Profil " + p.Name
	}
	return cfg.Retention.ReleasesKeep, "global"
}

// newReleaseForm prepares the block for rels. f is the submitted form (nil
// on first display); reset preselects from the profile instead of reading
// the boxes (the profile was changed).
func (s *Server) newReleaseForm(mode, action string, rels []sources.Release, f url.Values, reset bool) *releaseForm {
	cfg := s.Lib.Config()
	rf := &releaseForm{Mode: mode, Action: action, Profiles: cfg.ProfileNames(), Default: cfg.DefaultReleaseProfile, Releases: rels, Pinned: map[string]bool{}}
	if rf.Default == "" {
		rf.Default = config.StandardProfile
	}
	var names []string
	for _, r := range rels {
		for _, a := range r.Assets {
			names = append(names, a.Name)
		}
	}
	rf.Offer = assetclass.Describe(names)
	if mode == "profile" {
		rf.Offer = fullOffer()
	}
	if f != nil {
		rf.Profile = f.Get(fProfile)
		for _, v := range f[fPin] {
			rf.Pinned[v] = true
		}
	}
	prof, _ := cfg.Profile(rf.profileName())
	rf.Keep, rf.KeepFrom = keepOf(cfg, rf.profileName())
	if f == nil || reset || f.Get(fShown) == "" {
		rf.preselect(prof.Filter())
	} else {
		rf.fromForm(f)
		if n, err := strconv.Atoi(strings.TrimSpace(f.Get(fKeep))); err == nil && n >= 0 {
			rf.Keep = n
		}
	}
	return rf
}

// choice is what the block chose.
func (rf *releaseForm) choice(cfg config.Config) core.ReleaseChoice {
	c := core.ReleaseChoice{Files: rf.files(cfg)}
	if def, _ := keepOf(cfg, rf.profileName()); rf.Keep != def || rf.Own {
		keep := rf.Keep
		c.Keep = &keep
	}
	for _, r := range rf.Releases {
		if rf.Pinned[r.Tag] {
			c.Pinned = append(c.Pinned, r.Tag)
		}
	}
	return c
}

// addReleases refreshes the block of the add dialog.
func (s *Server) addReleases(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	link := strings.TrimSpace(r.Form.Get("url"))
	rels, err := s.Lib.ReleasesOf(r.Context(), link)
	if err != nil {
		s.partial(w, "release-choice", &releaseForm{Mode: "add"}) // renders nothing
		return
	}
	s.partial(w, "release-choice", s.addReleaseForm(rels, r.Form, profileChanged(r)))
}

// profileChanged reports whether the refresh came from the profile list
// (then the boxes are preselected from the new profile).
func profileChanged(r *http.Request) bool {
	return r.Header.Get("HX-Trigger-Name") == fProfile && r.Form.Get(fProfile) != ownProfile
}

// addReleaseForm is the block of the add dialog with its estimate.
func (s *Server) addReleaseForm(rels []sources.Release, f url.Values, reset bool) *releaseForm {
	rf := s.newReleaseForm("add", "/add/releases", rels, f, reset)
	est := s.Lib.EstimateChoice(rels, rf.choice(s.Lib.Config()))
	rf.Estimate = &est
	return rf
}

// repoReleaseForm is the block of a repository's settings, preselected
// from its choice unless f carries the boxes.
func (s *Server) repoReleaseForm(repo *core.Repo, rels []sources.Release, f url.Values, reset bool) *releaseForm {
	id := repo.R().ID
	rf := s.newReleaseForm("repo", "/repo/"+id+"/release-choice", rels, f, reset)
	if f == nil {
		cfg := s.Lib.Config()
		switch cur := repo.R().ReleaseFiles; {
		case cur == nil:
		case cur.Profile == "" && (len(cur.OS) > 0 || len(cur.Arch) > 0 || len(cur.Formats) > 0):
			rf.Profile = ownProfile
			rf.preselect(cur.Filter())
		default:
			rf.Profile = cur.Profile
			p, _ := cfg.Profile(rf.profileName())
			pf := p.Filter()
			pf.Include = append(slices.Clone(pf.Include), cur.Include...)
			pf.Exclude = append(slices.Clone(pf.Exclude), cur.Exclude...)
			rf.preselect(pf)
		}
	}
	c := rf.choice(s.Lib.Config())
	keep := s.Lib.EffectiveRetention(repo).ReleasesKeep
	c.Keep, c.Pinned = &keep, repo.R().Retention.Pinned
	est := s.Lib.EstimateChoice(rels, c)
	rf.Estimate = &est
	return rf
}

// fillReleaseFiles prepares the release section of the settings tab.
func (s *Server) fillReleaseFiles(r *http.Request, d *repoData) {
	rels, err := s.Lib.KnownReleases(r.Context(), d.Repo.R().ID)
	if err != nil || len(rels) == 0 {
		return
	}
	d.ReleaseForm = s.repoReleaseForm(d.Repo, rels, nil, false)
	d.Unwanted, _ = s.Lib.UnwantedFiles(r.Context(), d.Repo.R().ID)
	for _, it := range d.Unwanted {
		d.UnwantedSize += it.Size
	}
	d.AskUnwanted = r.URL.Query().Get("unwanted") == "1" && len(d.Unwanted) > 0
}

// repoReleaseChoice refreshes the block in a repository's settings.
func (s *Server) repoReleaseChoice(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	repo, err := s.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	rels, _ := s.Lib.KnownReleases(r.Context(), repo.R().ID)
	s.partial(w, "release-choice", s.repoReleaseForm(repo, rels, r.Form, profileChanged(r)))
}

// repoReleaseFiles saves the choice; the dialog that follows asks about
// files that no longer match (T-9.28).
func (s *Server) repoReleaseFiles(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	repo, err := s.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	rels, _ := s.Lib.KnownReleases(r.Context(), repo.R().ID)
	rf := s.newReleaseForm("repo", "", rels, r.Form, false)
	files := rf.files(s.Lib.Config())
	if _, err := s.Lib.SetReleaseFiles(r.Context(), repo.R().ID, files, repo.R().Retention.ReleasesKeep, s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+repo.R().ID+"?tab=settings&unwanted=1#release-files", "ok", "Auswahl gespeichert – gilt ab dem nächsten Abgleich."))
}

// repoUnwanted moves the files that no longer match to the trash, or keeps
// them (no longer proposed).
func (s *Server) repoUnwanted(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.PathValue("id")
	items, err := s.Lib.UnwantedFiles(r.Context(), id)
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	// Only items that are still proposals of this repository.
	var ids []string
	for _, it := range items {
		if slices.Contains(r.Form["item"], it.ID) {
			ids = append(ids, it.ID)
		}
	}
	defer s.invalidateReview()
	if r.Form.Get("action") == "keep" {
		if err := s.Lib.KeepItems(r.Context(), ids, s.actor(r)); err != nil {
			s.failErr(w, r, err)
			return
		}
		redirect(w, r, withMsg("/repo/"+id+"?tab=settings#release-files", "ok", "Behalten – die Dateien werden nicht mehr vorgeschlagen."))
		return
	}
	n, err := s.Lib.MoveToTrash(r.Context(), ids, s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+id+"?tab=settings#release-files", "ok", strconv.Itoa(n)+" Releases bereinigt – die Dateien liegen im Papierkorb."))
}

// ---- Release profiles in the settings (T-9.27) ----

// profileRow is one line of the profile list.
type profileRow struct {
	Name, Summary string
	Keep          string
	Uses          int
	Default       bool
}

// profileSummary describes a filter in a few words.
func profileSummary(f assetclass.Filter) string {
	if f.Empty() {
		return "alle Dateien"
	}
	var parts []string
	if len(f.OS) > 0 {
		var os []string
		for _, o := range f.OS {
			os = append(os, strings.SplitN(orKey(osText, o), " (", 2)[0])
		}
		parts = append(parts, strings.Join(os, ", "))
	}
	if len(f.Arch) > 0 {
		var as []string
		for _, a := range f.Arch {
			as = append(as, strings.SplitN(orKey(archText, a), " (", 2)[0])
		}
		parts = append(parts, strings.Join(as, ", "))
	}
	if len(f.Formats) > 0 {
		var fs []string
		for _, k := range f.Formats {
			o, fm, _ := strings.Cut(k, "/")
			fs = append(fs, strings.SplitN(orKey(osText, o), " (", 2)[0]+": "+orKey(formatText, fm))
		}
		parts = append(parts, strings.Join(fs, ", "))
	}
	if len(f.Include) > 0 {
		parts = append(parts, "zusätzlich "+strings.Join(f.Include, ", "))
	}
	if len(f.Exclude) > 0 {
		parts = append(parts, "ohne "+strings.Join(f.Exclude, ", "))
	}
	return strings.Join(parts, " · ")
}

// profileRows lists the profiles for the settings page.
func (s *Server) profileRows(r *http.Request) []profileRow {
	cfg := s.Lib.Config()
	uses := s.Lib.ProfileUsage(r.Context())
	def := cfg.DefaultReleaseProfile
	if def == "" {
		def = config.StandardProfile
	}
	var rows []profileRow
	for _, name := range cfg.ProfileNames() {
		p, _ := cfg.Profile(name)
		keep, _ := keepOf(cfg, name)
		k := strconv.Itoa(keep)
		if p.ReleasesKeep == nil {
			k += " (global)"
		}
		rows = append(rows, profileRow{Name: name, Summary: profileSummary(p.Filter()), Keep: k, Uses: uses[name], Default: name == def})
	}
	return rows
}

// profileData is the profile editor.
type profileData struct {
	Name   string
	New    bool
	Keep   string
	Form   *releaseForm
	Global int
}

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request) {
	cfg := s.Lib.Config()
	name := r.URL.Query().Get("name")
	d := profileData{New: name == "", Global: cfg.Retention.ReleasesKeep}
	rf := s.newReleaseForm("profile", "/settings/profile/choice", nil, nil, false)
	if !d.New {
		p, ok := cfg.Profile(name)
		if !ok || name == config.StandardProfile {
			s.fail(w, r, http.StatusNotFound, "Profil nicht gefunden.")
			return
		}
		d.Name = p.Name
		if p.ReleasesKeep != nil {
			d.Keep = strconv.Itoa(*p.ReleasesKeep)
		}
		rf.preselect(p.Filter())
	} else {
		// A new profile starts without systems: the user ticks what he
		// wants, which is the point of a profile.
		rf.preselect(assetclass.Filter{OS: []string{assetclass.Any}})
	}
	d.Form = rf
	title := "Release-Profil " + d.Name
	if d.New {
		title = "Neues Release-Profil"
	}
	s.render(w, r, "profile", title, "settings", d)
}

// profileChoice refreshes the block of the editor.
func (s *Server) profileChoice(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	s.partial(w, "release-choice", s.newReleaseForm("profile", "/settings/profile/choice", nil, r.Form, false))
}

// profileSave creates or changes a profile. Names are fixed once created:
// repositories refer to them.
func (s *Server) profileSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.Form
	cfg := s.Lib.Config()
	orig := f.Get("orig")
	p := config.ReleaseProfile{Name: strings.TrimSpace(f.Get("name"))}
	if orig != "" {
		p.Name = orig
	}
	p.ReleasesKeep = optInt(f.Get("keep"))
	flt := s.newReleaseForm("profile", "", nil, f, false).filter()
	p.OS, p.Arch, p.Formats, p.Include, p.Exclude = flt.OS, flt.Arch, flt.Formats, flt.Include, flt.Exclude
	profiles := slices.Clone(cfg.ReleaseProfiles)
	if i := slices.IndexFunc(profiles, func(x config.ReleaseProfile) bool { return x.Name == orig }); orig != "" && i >= 0 {
		profiles[i] = p
	} else {
		profiles = append(profiles, p)
	}
	cfg.ReleaseProfiles = profiles
	if err := s.Lib.SaveConfig(cfg); err != nil {
		s.failErr(w, r, err)
		return
	}
	msg := "Profil „" + p.Name + "“ gespeichert – gilt ab dem nächsten Abgleich."
	if n := s.Lib.UnwantedCount(r.Context()); n > 0 {
		msg += " " + strconv.Itoa(n) + " Releases haben gespeicherte Dateien, die nicht mehr passen – siehe „Aufräumen“."
	}
	redirect(w, r, withMsg("/settings#release-profile", "ok", msg))
}

// profileDelete removes a profile; repositories using it fall back to the
// default profile.
func (s *Server) profileDelete(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	name := r.Form.Get("name")
	cfg := s.Lib.Config()
	cfg.ReleaseProfiles = slices.DeleteFunc(slices.Clone(cfg.ReleaseProfiles), func(p config.ReleaseProfile) bool { return p.Name == name })
	if cfg.DefaultReleaseProfile == name {
		cfg.DefaultReleaseProfile = ""
	}
	if err := s.Lib.SaveConfig(cfg); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/settings#release-profile", "ok", "Profil „"+name+"“ entfernt. Repos, die es nutzten, folgen jetzt der Vorgabe."))
}

// profileDefault sets the profile for repositories without a choice.
func (s *Server) profileDefault(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	cfg := s.Lib.Config()
	cfg.DefaultReleaseProfile = r.Form.Get("default")
	if cfg.DefaultReleaseProfile == config.StandardProfile {
		cfg.DefaultReleaseProfile = ""
	}
	if err := s.Lib.SaveConfig(cfg); err != nil {
		s.failErr(w, r, err)
		return
	}
	msg := "Vorgabe gespeichert."
	if n := s.Lib.UnwantedCount(r.Context()); n > 0 {
		msg += " " + strconv.Itoa(n) + " Releases haben gespeicherte Dateien, die nicht mehr passen – siehe „Aufräumen“."
	}
	redirect(w, r, withMsg("/settings#release-profile", "ok", msg))
}
