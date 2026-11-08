// SPDX-License-Identifier: GPL-3.0-or-later

package logx

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"https://x-access-token:ghs_abcdefghijklmnopqrstuvwxyz@github.com/a/b": "https://***@github.com/a/b",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123 used":                        "token *** used",
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnop":                      "***",
		"Authorization: Bearer abc.def":                                        "Authorization: Bearer ***",
		"ssh://git@github.com/a/b":                                             "ssh://***@github.com/a/b",
		"plain message":                                                        "plain message",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoggerRedactsAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	big := bytes.Repeat([]byte("x"), maxBytes)
	os.WriteFile(path, big, 0o644)
	os.WriteFile(path+".1", []byte("one"), 0o644)

	var term bytes.Buffer
	log, c, err := Open(dir, slog.LevelInfo, &term)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("clone", "url", "https://u:ghp_abcdefghijklmnopqrstuvwxyz@github.com/a/b",
		"err", errors.New("auth failed for ghp_abcdefghijklmnopqrstuvwxyz"))
	log.Debug("hidden")
	c.Close()

	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "ghp_") || !strings.Contains(string(b), "***@github.com") {
		t.Errorf("log not redacted: %s", b)
	}
	if strings.Contains(string(b), "hidden") || term.Len() == 0 {
		t.Error("level or second writer wrong")
	}
	if got, _ := os.ReadFile(path + ".2"); string(got) != "one" {
		t.Error("old log not shifted")
	}
	if info, _ := os.Stat(path + ".1"); info.Size() != maxBytes {
		t.Error("big log not rotated")
	}
	Discard().Info("nothing")
}
