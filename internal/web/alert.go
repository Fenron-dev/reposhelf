// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// alertText titles the alert blocks of GitHub Markdown (> [!NOTE] …),
// with a symbol so the kind never depends on colour alone (T-9.25).
var alertText = map[string]string{
	"note": "ⓘ Hinweis", "tip": "✦ Tipp", "important": "❗ Wichtig", "warning": "▲ Warnung", "caution": "⛔ Vorsicht",
}

// alertClass is the class of every alert block; htmlsafe keeps exactly
// these classes (classRe there).
const alertClass = "markdown-alert"

// alerts turns GitHub alert blocks into styled blocks. Like GitHub it only
// takes a block quote on the top level whose first line is the marker
// alone and which has content after it; everything else stays a quote.
type alerts struct{}

func (alerts) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(util.Prioritized(alerts{}, alertPriority)))
}

// alertPriority: run after the transformers of the built-in extensions.
const alertPriority = 999

func (alerts) Transform(doc *ast.Document, r text.Reader, _ parser.Context) {
	src := r.Source()
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		if q, ok := n.(*ast.Blockquote); ok {
			alert(q, src)
		}
	}
}

func alert(q *ast.Blockquote, src []byte) {
	p, ok := q.FirstChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return
	}
	first := p.Lines().At(0)
	marker := strings.TrimSpace(string(first.Value(src)))
	if !strings.HasPrefix(marker, "[!") || !strings.HasSuffix(marker, "]") {
		return
	}
	kind := strings.ToLower(marker[2 : len(marker)-1])
	title, ok := alertText[kind]
	if !ok || (p.Lines().Len() == 1 && p.NextSibling() == nil) {
		return
	}
	// The marker line must be plain text (a reference definition could
	// make it a link); its nodes are dropped.
	end := first.Start + len(strings.TrimRight(string(first.Value(src)), " \t\r\n"))
	var marks []ast.Node
	for c := p.FirstChild(); ; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok {
			return // the line holds more than text, or ended early
		}
		marks = append(marks, c)
		if t.Segment.Stop >= end {
			break
		}
	}
	for _, c := range marks {
		p.RemoveChild(p, c)
	}
	if p.ChildCount() == 0 {
		q.RemoveChild(q, p)
	}
	head := ast.NewParagraph()
	head.SetAttributeString("class", []byte(alertClass+"-title"))
	head.AppendChild(head, ast.NewString([]byte(title)))
	q.InsertBefore(q, q.FirstChild(), head)
	q.SetAttributeString("class", []byte(alertClass+" "+alertClass+"-"+kind))
}
