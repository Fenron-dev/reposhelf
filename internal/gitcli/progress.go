// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ProgressFunc receives the current step of a long git operation and its
// overall percentage (0–100), for progress bars (T-9.9).
type ProgressFunc func(percent int)

type progressKey struct{}

// WithProgress attaches a progress callback to ctx; Mirror reports the
// fetch progress through it.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

func progressOf(ctx context.Context) ProgressFunc {
	fn, _ := ctx.Value(progressKey{}).(ProgressFunc)
	return fn
}

// git prints "Counting objects", "Compressing objects" (server side),
// then "Receiving objects" and "Resolving deltas". Receiving dominates
// the time, so it gets most of the bar.
var progressLine = regexp.MustCompile(`(Counting|Compressing|Receiving|Resolving)[^:]*:\s+(\d+)%`)

var phaseShare = map[string][2]int{ // start, width of the overall bar
	"Counting": {0, 5}, "Compressing": {5, 5}, "Receiving": {10, 80}, "Resolving": {90, 10},
}

// progressWriter parses git's stderr (lines end with \r while counting).
type progressWriter struct {
	buf  bytes.Buffer // everything, for error messages
	line []byte
	fn   ProgressFunc
	last int
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.buf.Write(b)
	for _, c := range b {
		if c == '\r' || c == '\n' {
			p.parse(string(p.line))
			p.line = p.line[:0]
			continue
		}
		p.line = append(p.line, c)
	}
	return len(b), nil
}

func (p *progressWriter) parse(line string) {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return
	}
	pct, _ := strconv.Atoi(m[2])
	share := phaseShare[m[1]]
	overall := share[0] + share[1]*pct/100
	if overall != p.last {
		p.last = overall
		p.fn(overall)
	}
}

// runProgress runs git like Run, streaming stderr through fn.
func (g *Git) runProgress(ctx context.Context, dir string, auth *Auth, fn ProgressFunc, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	pw := &progressWriter{fn: fn}
	cmd := g.command(ctx, dir, auth, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = io.Writer(pw)
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), classify(&Error{Args: args, Stderr: strings.TrimSpace(pw.buf.String()), Err: err}, ctx)
	}
	return stdout.Bytes(), nil
}
