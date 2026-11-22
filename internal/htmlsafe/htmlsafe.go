// SPDX-License-Identifier: GPL-3.0-or-later

// Package htmlsafe cleans HTML that comes from foreign repositories
// (README files rendered from Markdown) down to a fixed allowlist of
// elements and attributes, so READMEs look as on GitHub – centred logos,
// <img> with a width, <details> – without letting a repository run
// script, load foreign content or restyle the page around it.
//
// The input is parsed like a browser would (golang.org/x/net/html) and
// written out again from the tree, so unbalanced tags cannot close
// elements of the surrounding page. Links and images go through the
// caller's hooks (rewriting into RepoShelf); ids get a prefix so a
// README cannot take over ids the page uses. It depends on nothing else
// in RepoShelf.
package htmlsafe

import (
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// IDPrefix is put in front of every id (and same-page "#" link), like
// GitHub does.
const IDPrefix = "user-content-"

// Options are the caller's hooks; nil hooks keep the address.
type Options struct {
	// Link rewrites the address of a link ("" drops the attribute).
	// It only sees addresses without a scheme or with http, https or
	// mailto, and never "#…" (handled here).
	Link func(href string) string
	// Image rewrites the address of an image and may add a title ("" as
	// src drops the image). It only sees addresses without a scheme or
	// with http or https.
	Image func(src string) (newSrc, title string)
}

// dropped elements are removed with everything inside: they run code,
// embed foreign content, or parse their content differently (which
// could be abused to smuggle markup past the cleaning).
var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Template: true, atom.Noscript: true, atom.Noembed: true, atom.Noframes: true, atom.Textarea: true,
	atom.Title: true, atom.Xmp: true, atom.Plaintext: true, atom.Svg: true, atom.Math: true,
	atom.Select: true, atom.Form: true, atom.Button: true, atom.Frameset: true, atom.Frame: true,
	atom.Applet: true, atom.Audio: true, atom.Video: true, atom.Source: true, atom.Track: true,
	atom.Canvas: true, atom.Head: true, atom.Meta: true, atom.Link: true, atom.Base: true,
}

// allowed elements and the attributes they may keep (besides the global
// ones). Elements neither allowed nor dropped are left out, their content
// stays.
var allowed = map[atom.Atom][]string{
	atom.A: {"href", "name"}, atom.Abbr: nil, atom.B: nil, atom.Blockquote: nil, atom.Br: nil,
	atom.Caption: nil, atom.Center: nil, atom.Cite: nil, atom.Code: nil, atom.Dd: nil, atom.Del: nil,
	atom.Details: {"open"}, atom.Dfn: nil, atom.Div: nil, atom.Dl: nil, atom.Dt: nil, atom.Em: nil,
	atom.Figcaption: nil, atom.Figure: nil, atom.Font: nil,
	atom.H1: nil, atom.H2: nil, atom.H3: nil, atom.H4: nil, atom.H5: nil, atom.H6: nil,
	atom.Hr: nil, atom.I: nil, atom.Img: {"src", "alt", "width", "height"}, atom.Input: {"type", "checked"},
	atom.Ins: nil, atom.Kbd: nil, atom.Li: nil, atom.Mark: nil, atom.Ol: {"start"}, atom.P: nil,
	atom.Picture: nil, atom.Pre: nil, atom.Q: nil, atom.Rp: nil, atom.Rt: nil, atom.Ruby: nil,
	atom.S: nil, atom.Samp: nil, atom.Section: nil, atom.Small: nil, atom.Span: nil, atom.Strike: nil,
	atom.Strong: nil, atom.Sub: nil, atom.Summary: nil, atom.Sup: nil,
	atom.Table: nil, atom.Tbody: nil, atom.Td: {"colspan", "rowspan"}, atom.Tfoot: nil,
	atom.Th: {"colspan", "rowspan"}, atom.Thead: nil, atom.Tr: nil, atom.Tt: nil, atom.U: nil,
	atom.Ul: nil, atom.Var: nil, atom.Wbr: nil,
}

// global attributes every allowed element may keep.
var global = []string{"align", "title", "id", "dir", "lang"}

var (
	alignRe = regexp.MustCompile(`^(?i)(left|right|center|justify)$`)
	sizeRe  = regexp.MustCompile(`^\d{1,4}(\.\d+)?(%|px)?$`)
	numRe   = regexp.MustCompile(`^\d{1,4}$`)
	// styleRe: the only style kept is the table cell alignment Markdown
	// renderers write.
	styleRe = regexp.MustCompile(`^(?i)\s*text-align:\s*(left|right|center)\s*;?\s*$`)
	langRe  = regexp.MustCompile(`^[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8})*$`)
	idRe    = regexp.MustCompile(`^[\p{L}\p{N}_.:-]{1,100}$`)
)

// Clean returns the cleaned HTML of the fragment s.
func Clean(s string, o Options) string {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(s), ctx)
	if err != nil {
		return html.EscapeString(s)
	}
	var b strings.Builder
	for _, n := range nodes {
		o.write(&b, n)
	}
	return b.String()
}

func (o Options) write(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(html.EscapeString(n.Data))
		return
	case html.ElementNode:
	default: // comments, doctype
		return
	}
	// Only HTML elements: foreign ones (inside svg/math) are dropped above.
	if n.Namespace != "" || dropped[n.DataAtom] {
		return
	}
	extra, ok := allowed[n.DataAtom]
	if !ok {
		o.children(b, n)
		return
	}
	attrs, keep := o.attrs(n, extra)
	if !keep {
		return
	}
	b.WriteString("<" + n.Data)
	for _, a := range attrs {
		b.WriteString(" " + a.Key + `="` + html.EscapeString(a.Val) + `"`)
	}
	if isVoid(n.DataAtom) {
		b.WriteString("/>")
		return
	}
	b.WriteString(">")
	// The parser drops the first newline inside <pre>; keep a meaningful one.
	if n.DataAtom == atom.Pre && n.FirstChild != nil && n.FirstChild.Type == html.TextNode && strings.HasPrefix(n.FirstChild.Data, "\n") {
		b.WriteString("\n")
	}
	o.children(b, n)
	b.WriteString("</" + n.Data + ">")
}

func (o Options) children(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		o.write(b, c)
	}
}

// attrs filters the attributes of n; keep is false when the element must
// go (an image without a usable address, an input that is no checkbox).
func (o Options) attrs(n *html.Node, extra []string) (out []html.Attribute, keep bool) {
	ok := func(k string) bool {
		for _, a := range global {
			if a == k {
				return true
			}
		}
		for _, a := range extra {
			if a == k {
				return true
			}
		}
		return false
	}
	var title, hookTitle string
	hasSrc := false
	for _, a := range n.Attr {
		k, v := strings.ToLower(a.Key), strings.TrimSpace(a.Val)
		if a.Namespace != "" {
			continue
		}
		if k == "style" && (n.DataAtom == atom.Td || n.DataAtom == atom.Th) && styleRe.MatchString(v) {
			out = append(out, html.Attribute{Key: k, Val: v})
			continue
		}
		if !ok(k) {
			continue
		}
		switch k {
		case "align":
			if !alignRe.MatchString(v) {
				continue
			}
		case "width", "height":
			if !sizeRe.MatchString(v) {
				continue
			}
		case "colspan", "rowspan", "start":
			if !numRe.MatchString(v) {
				continue
			}
		case "lang":
			if !langRe.MatchString(v) {
				continue
			}
		case "dir":
			if v != "ltr" && v != "rtl" && v != "auto" {
				continue
			}
		case "id", "name":
			if !idRe.MatchString(v) || hasAttr(out, "id") {
				continue
			}
			k, v = "id", IDPrefix+v // <a name> was the old way to set an anchor
		case "type":
			if !strings.EqualFold(v, "checkbox") {
				return nil, false
			}
		case "checked", "open":
			v = ""
		case "title":
			if n.DataAtom == atom.Img {
				title = v
				continue // added after the hook, which may set its own
			}
		case "href":
			v = o.link(v)
			if v == "" {
				continue
			}
		case "src":
			src, t := o.image(v)
			if src == "" {
				return nil, false
			}
			v, hasSrc, hookTitle = src, true, t
		}
		out = append(out, html.Attribute{Key: k, Val: v})
	}
	switch n.DataAtom {
	case atom.Img:
		if !hasSrc {
			return nil, false
		}
		if hookTitle != "" {
			title = hookTitle
		}
		if title != "" {
			out = append(out, html.Attribute{Key: "title", Val: title})
		}
	case atom.Input:
		// Only the read-only task list boxes of Markdown.
		if !hasAttr(out, "type") {
			return nil, false
		}
		out = append(out, html.Attribute{Key: "disabled"})
	}
	return out, true
}

func hasAttr(as []html.Attribute, k string) bool {
	for _, a := range as {
		if a.Key == k {
			return true
		}
	}
	return false
}

func (o Options) link(v string) string {
	if strings.HasPrefix(v, "#") {
		if len(v) == 1 || strings.HasPrefix(v[1:], IDPrefix) {
			return v
		}
		return "#" + IDPrefix + v[1:]
	}
	if !safeScheme(v, "http", "https", "mailto") {
		return ""
	}
	if o.Link != nil {
		return o.Link(v)
	}
	return v
}

func (o Options) image(v string) (string, string) {
	if v == "" || !safeScheme(v, "http", "https") {
		return "", ""
	}
	if o.Image != nil {
		return o.Image(v)
	}
	return v, ""
}

// safeScheme reports whether v is relative or uses one of the schemes.
// The parser already decoded entities, so "jav&#x61;script:" arrives as
// "javascript:"; browsers also ignore tabs and newlines inside it.
func safeScheme(v string, schemes ...string) bool {
	clean := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r < 0x20 {
			return -1
		}
		return r
	}, v)
	u, err := url.Parse(clean)
	if err != nil {
		return false
	}
	if u.Scheme == "" {
		return true // relative, or "//host/x" (the hooks treat it as foreign)
	}
	for _, s := range schemes {
		if strings.EqualFold(u.Scheme, s) {
			return true
		}
	}
	return false
}

func isVoid(a atom.Atom) bool {
	switch a {
	case atom.Br, atom.Hr, atom.Img, atom.Input, atom.Wbr:
		return true
	}
	return false
}
