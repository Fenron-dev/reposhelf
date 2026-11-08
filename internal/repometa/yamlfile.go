// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"bytes"
	"fmt"
	"reflect"

	"go.yaml.in/yaml/v3"
)

// File is any YAML file of RepoShelf that people may edit by hand
// (reposhelf.yaml, collections). Like Document it keeps unknown keys,
// comments and order when written back (AGENTS.md invariant 6).
type File[T any] struct {
	Value T
	doc   *yaml.Node
}

// ParseFile decodes raw into a File. Unknown keys are allowed.
func ParseFile[T any](raw []byte, base T) (*File[T], error) {
	f := &File[T]{Value: base}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		if err := n.Content[0].Decode(&f.Value); err != nil {
			return nil, err
		}
		f.doc = &n
	}
	return f, nil
}

// NewFile wraps a value for a file that does not exist yet.
func NewFile[T any](v T) *File[T] { return &File[T]{Value: v} }

// Marshal encodes the value, merged into the parsed document.
func (f *File[T]) Marshal() ([]byte, error) {
	var fresh yaml.Node
	if err := fresh.Encode(&f.Value); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if f.doc == nil {
		f.doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&fresh}}
	} else {
		f.doc.Content[0] = merge(f.doc.Content[0], &fresh, reflect.TypeFor[T]())
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(f.doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
