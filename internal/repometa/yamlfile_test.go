// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"strings"
	"testing"
)

type sample struct {
	Name  string `yaml:"name"`
	Color string `yaml:"color"`
}

func TestFileKeepsUnknown(t *testing.T) {
	raw := "# Kommentar\nname: a\nextra: [1, 2]\ncolor: red\n"
	f, err := ParseFile([]byte(raw), sample{})
	if err != nil {
		t.Fatal(err)
	}
	f.Value.Color = "blue"
	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "# Kommentar") || !strings.Contains(s, "extra: [1, 2]") || !strings.Contains(s, "color: blue") {
		t.Errorf("Marshal = %s", s)
	}
	n := NewFile(sample{Name: "x"})
	if b, _ := n.Marshal(); !strings.Contains(string(b), "name: x") {
		t.Errorf("new = %s", b)
	}
	if _, err := ParseFile([]byte("a: [b"), sample{}); err == nil {
		t.Error("broken YAML accepted")
	}
}
