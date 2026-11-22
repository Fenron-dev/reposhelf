// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// TestReadmeImagesLocal: kept images come from RepoShelf, others become a
// placeholder – no request to foreign servers (V-4, R-023).
func TestReadmeImagesLocal(t *testing.T) {
	rw := &linkRewriter{repoID: "R1", ref: "HEAD", assets: map[string]core.ReadmeAsset{
		"https://img.example/logo.png": {File: "abc.png"},
	}}
	src := "![a](https://img.example/logo.png) ![b](https://tracker.example/pixel.gif) ![c](//cdn.example/x.png) ![d](docs/a.png)"
	out := string(rw.render([]byte(src)))
	for _, want := range []string{`src="/repo/R1/readme-asset/abc.png"`, `src="` + externalImage + `"`, `title="Externes Bild, nicht gesichert: https://tracker.example/pixel.gif"`, `src="/repo/R1/raw/docs/a.png?ref=HEAD"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, `src="https://tracker`) || strings.Contains(out, `src="//cdn`) {
		t.Error("foreign image still loaded")
	}
}

// TestReadmeHTML: a README in docs/ with HTML (like sherlock's) shows its
// images relative to docs/, centred, with kept foreign banners (R-029).
func TestReadmeHTML(t *testing.T) {
	rw := &linkRewriter{repoID: "R1", ref: "HEAD", base: "docs", assets: map[string]core.ReadmeAsset{
		"https://sponsors.example/banner": {File: "b.png"},
	}}
	src := `<p align="center">
  <a href="https://project.example/" target="_blank"><img src="images/logo.png" alt="logo"/></a>
</p>

<div align="center"><a href="https://sponsors.example/go"><img src="https://sponsors.example/banner" width="100%"></a></div>

## Installation

See [usage](usage.md#options) and [top](#installation). <img src=x onerror=alert(1)><script>alert(2)</script>
`
	out := string(rw.render([]byte(src)))
	for _, want := range []string{
		`<p align="center">`,
		`<img src="/repo/R1/raw/docs/images/logo.png?ref=HEAD" alt="logo"/>`,
		`<img src="/repo/R1/readme-asset/b.png" width="100%"/>`,
		`<h2 id="user-content-installation">Installation</h2>`,
		`href="/repo/R1/blob/docs/usage.md?ref=HEAD#user-content-options"`,
		`href="#user-content-installation"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	for _, bad := range []string{"onerror", "<script", "alert(2)", "target="} {
		if strings.Contains(out, bad) {
			t.Errorf("%s kept in\n%s", bad, out)
		}
	}
}
