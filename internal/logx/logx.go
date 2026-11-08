// SPDX-License-Identifier: GPL-3.0-or-later

// Package logx sets up RepoShelf's structured log (log/slog, T-0.8).
//
// Lines go to <data>/.reposhelf/logs/reposhelf.log as logfmt-like text and,
// optionally, to a second writer (the terminal). The file is rotated at
// start when it is large; three old files are kept. Every message and
// attribute passes Redact, so tokens never reach a log (AGENTS.md security).
//
// Error concept: packages wrap errors with context (fmt.Errorf %w) and use
// sentinel errors for kinds the user interface must tell apart (not found,
// invalid input, conflict, upstream unavailable). Only the outermost layer
// (CLI, web, MCP) turns them into German messages; logs stay English.
package logx

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileName of the current log file.
const FileName = "reposhelf.log"

const (
	maxBytes = 10 << 20
	keep     = 3
	filePerm = 0o644
)

// Open creates the logger. Close the returned closer on exit.
func Open(dir string, level slog.Leveler, extra io.Writer) (*slog.Logger, io.Closer, error) {
	path := filepath.Join(dir, FileName)
	if err := rotate(path); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	var w io.Writer = f
	if extra != nil {
		w = io.MultiWriter(f, extra)
	}
	return New(w, level), f, nil
}

// New returns a redacting text logger writing to w.
func New(w io.Writer, level slog.Leveler) *slog.Logger {
	h := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Value.Kind() == slog.KindString || a.Value.Kind() == slog.KindAny {
				a.Value = slog.StringValue(Redact(a.Value.String()))
			}
			return a
		},
	})
	return slog.New(h)
}

// Discard is a logger that writes nothing (tests, quiet CLI commands).
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func rotate(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxBytes {
		return nil
	}
	for i := keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", path, i)
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, fmt.Sprintf("%s.%d", path, i+1)); err != nil {
				return fmt.Errorf("rotate log: %w", err)
			}
		}
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return fmt.Errorf("rotate log: %w", err)
	}
	return nil
}

var (
	// userinfo in URLs: https://user:token@host → https://***@host
	urlCreds = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
	// Token formats of GitHub, GitLab and generic bearer headers.
	tokens = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|rs_[A-Za-z0-9]{20,})\b`)
	bearer = regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|token|basic)\s+)\S+`)
)

// Redact removes credentials from s.
func Redact(s string) string {
	if !strings.ContainsAny(s, "@_-:") {
		return s
	}
	s = urlCreds.ReplaceAllString(s, "${1}***@")
	s = tokens.ReplaceAllString(s, "***")
	return bearer.ReplaceAllString(s, "${1}***")
}
