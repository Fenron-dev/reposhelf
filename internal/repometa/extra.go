// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"go.yaml.in/yaml/v3"
)

// Extra returns a top-level key that the schema does not define (e.g. a
// hand-written "description" of a local repository).
func (d *Document) Extra(key string) (string, bool) {
	if d.doc == nil || len(d.doc.Content) == 0 {
		return "", false
	}
	if _, known := fieldTypes(typeOfRepo)[key]; known {
		return "", false
	}
	v := lookup(d.doc.Content[0], key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return "", false
	}
	return v.Value, true
}

// SetExtra sets a top-level key that the schema does not define. An empty
// value removes it. Known keys cannot be set this way.
func (d *Document) SetExtra(key, value string) error {
	if _, known := fieldTypes(typeOfRepo)[key]; known {
		return &FieldError{Field: key, Problem: "is a schema field"}
	}
	if d.doc == nil {
		if _, err := d.Marshal(); err != nil {
			return err
		}
	}
	m := d.doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if value == "" {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
			} else {
				m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: value}
			}
			return nil
		}
	}
	if value != "" {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
	return nil
}
