// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"strings"
	"testing"
)

func TestAlerts(t *testing.T) {
	rw := &linkRewriter{repoID: "R1", ref: "HEAD"}
	render := func(src string) string { return string(rw.render([]byte(src))) }
	cases := []struct{ name, src, want string }{
		{"note", "> [!NOTE]\n> Text **fett**.\n",
			"<blockquote class=\"markdown-alert markdown-alert-note\"><p class=\"markdown-alert-title\">ⓘ Hinweis</p>\n<p>Text <strong>fett</strong>.</p>"},
		{"lower case and paragraph", "> [!warning]  \n>\n> Erst lesen.\n",
			"<blockquote class=\"markdown-alert markdown-alert-warning\"><p class=\"markdown-alert-title\">▲ Warnung</p>\n<p>Erst lesen.</p>"},
		{"caution with list", "> [!CAUTION]\n> - eins\n",
			`<blockquote class="markdown-alert markdown-alert-caution"><p class="markdown-alert-title">⛔ Vorsicht</p>`},
		{"unknown kind stays quote", "> [!FOO]\n> Text\n", "<blockquote>\n<p>[!FOO]\nText</p>"},
		{"marker only stays quote", "> [!TIP]\n", "<blockquote>\n<p>[!TIP]</p>"},
		{"marker not alone stays quote", "> [!NOTE] Text\n", "<blockquote>\n<p>[!NOTE] Text</p>"},
		{"nested stays quote", "- > [!NOTE]\n  > Text\n", "<blockquote>\n<p>[!NOTE]\nText</p>"},
		{"marker as link stays quote", "> [!NOTE]\n> Text\n\n[!note]: https://x.example/\n", `<blockquote>`},
	}
	for _, c := range cases {
		got := render(c.src)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: missing %q in\n%s", c.name, c.want, got)
		}
		if strings.HasSuffix(c.name, "stays quote") && strings.Contains(got, "markdown-alert") {
			t.Errorf("%s: became an alert:\n%s", c.name, got)
		}
	}
}

func TestAlertClassesOnly(t *testing.T) {
	rw := &linkRewriter{repoID: "R1", ref: "HEAD"}
	got := string(rw.render([]byte(`<p class="modal-backdrop">x</p><blockquote class="markdown-alert markdown-alert-tip">y</blockquote>`)))
	if strings.Contains(got, "modal-backdrop") {
		t.Errorf("foreign class kept: %s", got)
	}
	if !strings.Contains(got, `<blockquote class="markdown-alert markdown-alert-tip">`) {
		t.Errorf("alert class dropped: %s", got)
	}
}
