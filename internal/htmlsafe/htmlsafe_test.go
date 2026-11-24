// SPDX-License-Identifier: GPL-3.0-or-later

package htmlsafe

import (
	"strings"
	"testing"
)

func TestCleanKeepsReadmeMarkup(t *testing.T) {
	in := `<p align="center"><a href="https://x.example/" target="_blank"><img src="images/logo.png" alt="logo" width="70%" height="70%"/></a></p>` +
		`<details open><summary>Mehr</summary><b>fett</b></details><h2 id="install">Install</h2><a href="#install">hier</a>` +
		`<table><tr><td style="text-align: center" colspan="2">x</td></tr></table><input type="checkbox" checked disabled>`
	got := Clean(in, Options{})
	for _, want := range []string{
		`<p align="center"><a href="https://x.example/"><img src="images/logo.png" alt="logo" width="70%" height="70%"/></a></p>`,
		`<details open=""><summary>Mehr</summary><b>fett</b></details>`,
		`<h2 id="user-content-install">Install</h2><a href="#user-content-install">hier</a>`,
		`<td style="text-align: center" colspan="2">x</td>`,
		`<input type="checkbox" checked="" disabled=""/>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, "target") {
		t.Errorf("target kept: %s", got)
	}
}

func TestCleanRemovesDanger(t *testing.T) {
	cases := map[string]string{
		`<script>alert(1)</script>ok`:                      "ok",
		`<img src=x onerror=alert(1)>`:                     `<img src="x"/>`,
		`<a href="javascript:alert(1)">a</a>`:              `<a>a</a>`,
		`<a href="jav&#x61;script:alert(1)">a</a>`:         `<a>a</a>`,
		"<a href=\"java\tscript:alert(1)\">a</a>":          `<a>a</a>`,
		`<a href="data:text/html,x">a</a>`:                 `<a>a</a>`,
		`<img src="data:image/png;base64,AAAA">`:           ``,
		`<style>body{display:none}</style>x`:               "x",
		`<div style="position:fixed">x</div>`:              `<div>x</div>`,
		`<div class="toast">x</div>`:                       `<div>x</div>`,
		`<iframe src="https://e.example"></iframe>`:        ``,
		`<svg><script>alert(1)</script></svg>`:             ``,
		`<form action="/logout"><button>x</button></form>`: ``,
		`</div></div><p>x`:                                 `<p>x</p>`,
		`<b><i>x</b>`:                                      `<b><i>x</i></b>`,
		`<!-- c --><input type="text" value="x">`:          ``,
		`<a name="top">t</a>`:                              `<a id="user-content-top">t</a>`,
		`<p id="a b">x</p>`:                                `<p>x</p>`,
		`<img src="x" width="100;position:fixed">`:         `<img src="x"/>`,
	}
	// Parsed like a browser with scripting on: the attribute ends the
	// noscript early, the img that follows is kept – without its handler.
	if got := Clean(`<noscript><p title="</noscript><img src=x onerror=alert(1)>"></p></noscript>`, Options{}); strings.Contains(got, "onerror") || strings.Contains(got, "<noscript") {
		t.Errorf("noscript = %q", got)
	}
	for in, want := range cases {
		if got := Clean(in, Options{}); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanHooks(t *testing.T) {
	o := Options{
		Link: func(h string) string { return "/L/" + h },
		Image: func(s string) (string, string) {
			if strings.HasPrefix(s, "https://") {
				return "/ph.svg", "fremd"
			}
			return "/R/" + s, ""
		},
	}
	got := Clean(`<a href="docs/x.md">a</a><a href="#top">t</a><img src="https://t.example/p.gif" title="orig"><img src="a.png">`, o)
	want := `<a href="/L/docs/x.md">a</a><a href="#user-content-top">t</a><img src="/ph.svg" title="fremd"/><img src="/R/a.png"/>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCleanPreNewline(t *testing.T) {
	if got := Clean("<pre>\n\nx</pre>", Options{}); got != "<pre>\n\nx</pre>" {
		t.Errorf("pre = %q", got)
	}
}

func TestCleanClass(t *testing.T) {
	got := Clean(`<blockquote class="markdown-alert markdown-alert-note"><p class="markdown-alert-title">T</p></blockquote>`+
		`<p class="btn">a</p><div class="markdown-alert">b</div><blockquote class="markdown-alert markdown-alert-x">c</blockquote>`, Options{})
	want := `<blockquote class="markdown-alert markdown-alert-note"><p class="markdown-alert-title">T</p></blockquote>` +
		`<p>a</p><div>b</div><blockquote>c</blockquote>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
