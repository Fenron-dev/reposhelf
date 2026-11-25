// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"fmt"
	"html/template"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// German labels (D-009). Status always comes with a symbol, never colour
// alone (design/README.md).
var (
	statusText = map[string]string{
		"ok": "Aktuell", "degraded": "Gestört", "gone": "Upstream weg", "archived": "Archiviert",
		"local": "Nur lokal", "pending": "Noch nicht abgeglichen",
	}
	statusSymbol = map[string]string{
		"ok": "✓", "degraded": "◐", "gone": "▲", "archived": "■", "local": "●", "pending": "○",
	}
	kindText = map[string]string{"own": "Eigen", "mirror": "Spiegel", "fork": "Fork"}
	modeText = map[string]string{"follow": "Folgen", "once": "Einmalig", "manual": "Manuell"}
	relText  = map[string]string{
		"plugin_of": "Plugin von", "depends_on": "hängt ab von", "fork_of": "Fork von", "successor_of": "Nachfolger von",
		"delivers_to": "liefert an", "uses_code_from": "nutzt Code von", "related": "verwandt mit",
	}
	relBackText = map[string]string{
		"plugin_of": "Plugin", "depends_on": "wird genutzt von", "fork_of": "Fork", "successor_of": "Vorgänger von",
		"delivers_to": "bekommt von", "uses_code_from": "Code genutzt von", "related": "verwandt mit",
	}
	relSourceText = map[string]string{
		"readme": "erkannt aus README", "dependency": "erkannt aus Abhängigkeiten", "fork": "Fork-Herkunft",
		"wikilink": "Wiki-Link in Notizen", "notes": "Link in Notizen", "manual": "von Hand", "submodule": "Submodul",
	}
)

// Labels of the choice of release files (D-035).
var (
	osText = map[string]string{
		"windows": "Windows", "macos": "macOS", "linux": "Linux", "bsd": "BSD", "android": "Android", "ios": "iOS",
		"any": "Plattformunabhängig (Prüfsummen, Quellcode, Web …)",
	}
	archText = map[string]string{
		"x64": "x64 (Intel/AMD, 64 Bit)", "arm64": "ARM64 (Apple Silicon, Snapdragon, Raspberry Pi 4/5)", "x86": "x86 (32 Bit)",
		"arm": "ARM 32 Bit", "riscv64": "RISC-V", "other": "Sonstige (s390x, PowerPC …)",
	}
	formatText = map[string]string{
		"appimage": "AppImage", "deb": ".deb (Debian, Ubuntu)", "rpm": ".rpm (Fedora, openSUSE)", "flatpak": "Flatpak",
		"snap": "Snap", "archpkg": "Arch-Paket", "msi": "Installer (.msi)", "msix": "MSIX/Store", "exe": "Programm/Installer (.exe)",
		"dmg": "Abbild (.dmg)", "pkg": "Installer (.pkg)", "apk": "APK", "aab": "App-Bundle (.aab)", "ipa": "IPA",
		"archive": "Archiv/portabel (zip, tar.gz …)", "script": "Installationsskript", "symbols": "Debug-Symbole",
		"source": "Quellcode", "other": "Sonstige",
	}
)

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"size":        humanSize,
		"static":      staticURL,
		"limitGB":     limitGB,
		"sub":         func(a, b int) int { return a - b },
		"min":         func(a, b int) int { return min(a, b) },
		"perMonth":    func(perDay float64) int64 { return int64(perDay * daysPerMonth) },
		"ago":         ago,
		"dt":          dt,
		"date":        func(t time.Time) string { return t.Local().Format("02.01.2006") },
		"statusText":  func(s string) string { return orKey(statusText, s) },
		"statusSym":   func(s string) string { return orKey(statusSymbol, s) },
		"kindText":    func(s string) string { return orKey(kindText, s) },
		"modeText":    func(s string) string { return orKey(modeText, s) },
		"relText":     func(s string) string { return orKey(relText, s) },
		"relBackText": func(s string) string { return orKey(relBackText, s) },
		"relSource":   func(s string) string { return orKey(relSourceText, s) },
		"osText":      func(s string) string { return orKey(osText, s) },
		"archText":    func(s string) string { return orKey(archText, s) },
		"formatText":  func(s string) string { return orKey(formatText, s) },
		"join":        strings.Join,
		"has":         func(list []string, v string) bool { return slices.Contains(list, v) },
		"hasPrefix":   strings.HasPrefix,
		"lower":       strings.ToLower,
		"add":         func(a, b int) int { return a + b },
		"deref":       derefAny,
		"dur":         func(d repometa.Duration) string { return durString(d) },
		"filterURL":   filterURL,
		"pathEsc":     url.PathEscape,
		"queryEsc":    url.QueryEscape,
		"short":       func(s string) string { return shortHash(s) },
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
		"anyStr": func(v any) string { return valueString(v) },
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"timeOf": func(t *time.Time) time.Time {
			if t == nil {
				return time.Time{}
			}
			return *t
		},
		"intervalText": func(e store.Entry) string {
			switch e.SyncMode {
			case "follow":
				return "alle " + durString(repometa.Duration(e.Interval))
			case "once":
				return "einmalig"
			}
			return "manuell"
		},
	}
}

func orKey(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return k
}

func humanSize(v any) string {
	var b int64
	switch t := v.(type) {
	case int64:
		b = t
	case int:
		b = int64(t)
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return strings.Replace(fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp]), ".", ",", 1)
}

func ago(v any) string {
	var t time.Time
	switch x := v.(type) {
	case *time.Time:
		if x == nil {
			return "nie"
		}
		t = *x
	case time.Time:
		t = x
	}
	if t.IsZero() {
		return "nie"
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		s = "wenigen Sekunden"
	case d < time.Hour:
		s = fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		s = fmt.Sprintf("%d h", int(d.Hours()))
	default:
		s = fmt.Sprintf("%d Tagen", int(d.Hours()/24))
	}
	if future {
		return "in " + s
	}
	return "vor " + s
}

func dt(v any) string {
	switch x := v.(type) {
	case *time.Time:
		if x == nil {
			return "–"
		}
		return x.Local().Format("02.01.2006 15:04")
	case time.Time:
		if x.IsZero() {
			return "–"
		}
		return x.Local().Format("02.01.2006 15:04")
	}
	return "–"
}

func derefAny(v any) string {
	switch x := v.(type) {
	case *int:
		if x == nil {
			return ""
		}
		return fmt.Sprint(*x)
	case *repometa.Duration:
		if x == nil {
			return ""
		}
		return x.String()
	case *string:
		if x == nil {
			return ""
		}
		return *x
	case *repometa.UpstreamStatus:
		if x == nil {
			return ""
		}
		return string(*x)
	}
	return ""
}

func durString(d repometa.Duration) string {
	if d <= 0 {
		return "aus"
	}
	t := time.Duration(d)
	days := int(t / (24 * time.Hour))
	t -= time.Duration(days) * 24 * time.Hour
	var parts []string
	switch days {
	case 0:
	case 1:
		parts = append(parts, "1 Tag")
	default:
		parts = append(parts, fmt.Sprintf("%d Tage", days))
	}
	if h := int(t / time.Hour); h > 0 {
		parts = append(parts, fmt.Sprintf("%d h", h))
		t -= time.Duration(h) * time.Hour
	}
	if m := int(t / time.Minute); m > 0 {
		parts = append(parts, fmt.Sprintf("%d min", m))
	}
	return strings.Join(parts, " ")
}

// filterURL builds a library URL that sets key=val on top of the current
// query (or removes key when val is empty).
func filterURL(q store.Query, view, key, val string) string {
	v := url.Values{}
	set := func(k, x string) {
		if x != "" {
			v.Set(k, x)
		}
	}
	set("q", q.Text)
	set("kind", q.Kind)
	set("tag", q.Tag)
	set("status", q.Status)
	set("collection", q.Collection)
	set("language", q.Language)
	set("license", q.License)
	if q.Buildable {
		v.Set("buildable", "1")
	}
	if q.Sort != store.SortUpdated {
		set("sort", q.Sort)
	}
	if view != "list" {
		set("view", view)
	}
	if key == "all" {
		v = url.Values{}
		if view != "list" {
			set("view", view)
		}
	} else if val == "" {
		v.Del(key)
	} else {
		v.Set(key, val)
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

func shortHash(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []any:
		var parts []string
		for _, p := range x {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}
