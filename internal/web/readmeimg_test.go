// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/util"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// TestReadmeImagesLocal: kept images come from RepoShelf, others become a
// placeholder – no request to foreign servers (V-4, R-023).
func TestReadmeImagesLocal(t *testing.T) {
	rw := &linkRewriter{repoID: "R1", ref: "HEAD", assets: map[string]core.ReadmeAsset{
		"https://img.example/logo.png": {File: "abc.png"},
	}}
	md := goldmark.New(goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(rw, 100))))
	var buf bytes.Buffer
	src := "![a](https://img.example/logo.png) ![b](https://tracker.example/pixel.gif) ![c](//cdn.example/x.png) ![d](docs/a.png)"
	if err := md.Convert([]byte(src), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`src="/repo/R1/readme-asset/abc.png"`, `src="` + externalImage + `"`, `title="Externes Bild, nicht gesichert: https://tracker.example/pixel.gif"`, `src="/repo/R1/raw/docs/a.png?ref=HEAD"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "tracker.example/pixel.gif\"") && strings.Contains(out, `src="https://tracker`) {
		t.Error("foreign image still loaded")
	}
}
