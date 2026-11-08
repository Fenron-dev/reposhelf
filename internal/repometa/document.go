// SPDX-License-Identifier: GPL-3.0-or-later

// Package repometa reads and writes repo.yaml, the metadata file that is the
// source of truth for one repository (D-004, docs/SCHEMA.md).
//
// The file is meant to be edited by hand and by newer RepoShelf versions, so
// writing is a merge, not a rewrite: the parsed YAML tree is kept, and on
// Marshal only the keys this version knows are updated. Unknown keys, their
// comments and the key order survive (AGENTS.md invariant 6).
//
// This package turns bytes into a validated Repo and back. Writing the bytes
// to disk atomically is the job of the atomic-write helper (T-0.5); paths
// built from Source must go through the path sanitizer of T-0.5.
//
// Depends only on the standard library and go.yaml.in/yaml/v3.
package repometa

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"
)

const (
	// FileName is the metadata file inside meta/<host>/<owner>/<name>/.
	FileName = "repo.yaml"
	// maxFileBytes guards against reading something that is clearly not a
	// metadata file (a misplaced archive, a runaway generator).
	maxFileBytes = 1 << 20
	yamlIndent   = 2
)

// Errors returned while parsing.
var (
	ErrNewerSchema = errors.New("repo.yaml was written by a newer RepoShelf")
	ErrNoSchema    = errors.New("repo.yaml has no schema number")
	ErrTooLarge    = errors.New("repo.yaml is too large")
)

// Document is a repo.yaml together with the YAML tree it was read from.
type Document struct {
	Repo Repo
	// Warnings lists unknown keys found while parsing. They are kept on
	// write; the warning only helps to spot typos.
	Warnings []string

	doc *yaml.Node // nil for documents created with New
}

// New wraps a Repo that was not read from a file.
func New(r Repo) *Document { return &Document{Repo: r} }

// Defaults returns the settings for a new repository of the given kind
// (KONZEPT 5.2: issues are on for own projects only).
func Defaults(kind Kind) Repo {
	return Repo{
		Schema: CurrentSchema,
		Kind:   kind,
		Include: Include{
			Releases:   true,
			LFS:        true,
			Submodules: true,
			Issues:     kind == KindOwn,
		},
	}
}

// Load reads and parses a repo.yaml file.
func Load(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxFileBytes {
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", path, ErrTooLarge, maxFileBytes)
	}
	d, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, nil
}

// Parse reads a repo.yaml, migrates it to CurrentSchema and validates it.
func Parse(raw []byte) (*Document, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: top level must be a mapping", ErrInvalid)
	}
	root := doc.Content[0]

	version, err := schemaOf(root)
	if err != nil {
		return nil, err
	}
	if err := migrate(root, version); err != nil {
		return nil, err
	}

	// Decode kind first: the defaults for absent keys depend on it.
	var head struct {
		Kind Kind `yaml:"kind"`
	}
	if err := root.Decode(&head); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	r := Defaults(head.Kind)
	if err := root.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	for i := range r.Relations {
		if r.Relations[i].State == "" {
			r.Relations[i].State = RelationConfirmed
		}
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}

	return &Document{Repo: r, Warnings: unknownKeys(root, typeOfRepo, ""), doc: &doc}, nil
}

// Marshal validates the Repo and returns the file content. For a parsed
// document the original tree is updated in place, so unknown keys, comments
// and key order are kept.
func (d *Document) Marshal() ([]byte, error) {
	if err := d.Repo.Validate(); err != nil {
		return nil, err
	}
	var fresh yaml.Node
	if err := fresh.Encode(&d.Repo); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	if d.doc == nil {
		d.doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&fresh}}
	} else {
		d.doc.Content[0] = merge(d.doc.Content[0], &fresh, typeOfRepo)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(d.doc); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

// schemaOf reads the schema number without decoding the rest, because the
// rest may follow an older layout that only migrate understands.
func schemaOf(root *yaml.Node) (int, error) {
	n := lookup(root, "schema")
	if n == nil || n.Tag == "!!null" {
		return 0, ErrNoSchema
	}
	v, err := strconv.Atoi(n.Value)
	switch {
	case err != nil || v < 1:
		return 0, fmt.Errorf("%w: schema must be a positive number, is %q", ErrInvalid, n.Value)
	case v > CurrentSchema:
		return 0, fmt.Errorf("%w (schema %d, this version supports up to %d)", ErrNewerSchema, v, CurrentSchema)
	}
	return v, nil
}

// lookup returns the value node of key in a mapping node, or nil.
func lookup(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
