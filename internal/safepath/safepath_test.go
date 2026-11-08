// SPDX-License-Identifier: GPL-3.0-or-later

package safepath

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
)

func TestCheckSegmentAccepts(t *testing.T) {
	for _, s := range []string{
		"github.com", "Fenron-dev", "Fero", "reposhelf.yaml", "v0.4.0",
		"my_repo", "a b", ".github", ".gitignore", "...x", "x.git",
		"Grüße", "日本語", "C++", "100%", "release (final)", "-dash",
		"CONSOLE", "COM10", "LPTX", "nul-device", "x.con", "aux_", ".reposhelf",
		strings.Repeat("x", MaxSegmentBytes),
		strings.Repeat("ü", MaxSegmentBytes/2), // 254 bytes
	} {
		if err := CheckSegment(s); err != nil {
			t.Errorf("CheckSegment(%q) = %v, want nil", s, err)
		}
	}
}

func TestCheckSegmentRejects(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "empty"},
		{".", ". or .."},
		{"..", ". or .."},
		{strings.Repeat("x", MaxSegmentBytes+1), "longer"},
		{strings.Repeat("ü", MaxSegmentBytes/2+1), "longer"},
		{"bad\xffutf8", "UTF-8"},
		{"a\x00b", "control"},
		{"line\nbreak", "control"},
		{"tab\t", "control"},
		{"del\x7f", "control"},
		{"c1\u0085", "control"},
		{"a/b", "/"},
		{"a\\b", "/"},
		{"/", "/"},
		{"../etc", "/"},
		{"..\\windows", "/"},
		{"host:3000", "<>:"},
		{"C:", "<>:"},
		{"what?", "<>:"},
		{"star*", "<>:"},
		{`quote"`, "<>:"},
		{"pipe|", "<>:"},
		{"<tag>", "<>:"},
		{" lead", "spaces"},
		{"trail ", "spaces"},
		{"\u00a0nbsp", "spaces"},
		{"dot.", "dot"},
		{"CON", "reserved"},
		{"con", "reserved"},
		{"Nul.txt", "reserved"},
		{"aux.tar.gz", "reserved"},
		{"COM1", "reserved"},
		{"lpt9.log", "reserved"},
		{"COM0", "reserved"},
		{"COM¹", "reserved"},
		{"lpt³.x", "reserved"},
		{"PRN .txt", "reserved"},
		{"CONIN$", "reserved"},
		{"conout$", "reserved"},
		{atomicfile.TempPrefix + "123", atomicfile.TempPrefix},
	}
	for _, c := range cases {
		err := CheckSegment(c.in)
		if err == nil {
			t.Errorf("CheckSegment(%q) = nil, want error", c.in)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("CheckSegment(%q) does not wrap ErrInvalid", c.in)
		}
		var se *SegmentError
		if !errors.As(err, &se) || se.Segment != c.in {
			t.Errorf("CheckSegment(%q) = %#v, want *SegmentError for the input", c.in, err)
			continue
		}
		if !strings.Contains(se.Problem, c.want) {
			t.Errorf("CheckSegment(%q) problem = %q, want it to mention %q", c.in, se.Problem, c.want)
		}
	}
}

func TestSplit(t *testing.T) {
	segs, err := Split("group/sub/team")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(segs, "|") != "group|sub|team" {
		t.Errorf("Split = %q", segs)
	}
	for _, in := range []string{"", "a//b", "/a", "a/", "a/../b", "a/CON"} {
		if _, err := Split(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Split(%q) = %v, want ErrInvalid", in, err)
		}
	}
}

func TestJoin(t *testing.T) {
	base := filepath.Join(t.TempDir(), "RepoShelf-Daten")

	got, err := Join(base, "meta", "github.com", "Fenron-dev", "Fero", "repo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "meta", "github.com", "Fenron-dev", "Fero", "repo.yaml")
	if got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}

	if got, err := Join(base); err != nil || got != base {
		t.Errorf("Join(base) = %q, %v", got, err)
	}

	for _, segs := range [][]string{
		{".."},
		{"meta", "..", "..", "etc"},
		{"meta", "../../etc"},
		{"a/b"},
		{""},
		{"C:"},
	} {
		if p, err := Join(base, segs...); !errors.Is(err, ErrInvalid) {
			t.Errorf("Join(base, %q) = %q, %v; want ErrInvalid", segs, p, err)
		}
	}

	if _, err := Join("", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Join with empty base = %v, want ErrInvalid", err)
	}
}

func FuzzJoinStaysInside(f *testing.F) {
	for _, s := range []string{"..", "a/../..", "x", "CON", "\x00", "a\\..\\..", "C:\\"} {
		f.Add(s)
	}
	base := filepath.Join("data", "root")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Join(base, s)
		if err != nil {
			return
		}
		rel, err := filepath.Rel(base, p)
		if err != nil || rel != s || filepath.Dir(p) != base {
			t.Fatalf("Join(%q, %q) = %q escapes or changes the name", base, s, p)
		}
	})
}
