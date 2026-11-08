// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var (
	typeOfRepo    = reflect.TypeFor[Repo]()
	marshalerType = reflect.TypeFor[yaml.Marshaler]()
	timeType      = reflect.TypeFor[time.Time]()
)

// identityKeys names the fields that identify a list entry, so an entry keeps
// its unknown keys even when the list is reordered or shortened. Lists of
// other structs would lose unknown keys inside entries; add them here.
var identityKeys = map[reflect.Type][]string{
	reflect.TypeFor[Relation](): {"type", "target"},
}

// merge returns orig updated with the values of upd, which is the fresh
// encoding of a value of type t. Keys that t knows are taken from upd (and
// dropped if upd has none); keys t does not know are kept. Unchanged nodes
// are kept as they are, so quoting, flow style and comments survive.
func merge(orig, upd *yaml.Node, t reflect.Type) *yaml.Node {
	t = deref(t)
	if isNull(orig) || isNull(upd) || orig.Kind != upd.Kind {
		return replace(orig, upd)
	}

	switch {
	case t.Kind() == reflect.Struct && !isScalarType(t) && orig.Kind == yaml.MappingNode:
		return mergeMapping(orig, upd, t)
	case t.Kind() == reflect.Slice && orig.Kind == yaml.SequenceNode:
		return mergeSequence(orig, upd, t.Elem())
	case orig.Kind == yaml.ScalarNode && orig.Value == upd.Value:
		return orig
	default:
		return replace(orig, upd)
	}
}

func mergeMapping(orig, upd *yaml.Node, t reflect.Type) *yaml.Node {
	fields := fieldTypes(t)
	updVals := map[string]*yaml.Node{}
	var updOrder []string
	for i := 0; i+1 < len(upd.Content); i += 2 {
		k := upd.Content[i].Value
		updVals[k] = upd.Content[i+1]
		updOrder = append(updOrder, k)
	}

	var out []*yaml.Node
	used := map[string]bool{}
	for i := 0; i+1 < len(orig.Content); i += 2 {
		key, val := orig.Content[i], orig.Content[i+1]
		ft, known := fields[key.Value]
		if !known {
			out = append(out, key, val)
			continue
		}
		u, present := updVals[key.Value]
		if !present || used[key.Value] {
			// Known key the struct no longer writes (omitempty) or a
			// duplicate key: the struct is authoritative, drop it.
			continue
		}
		used[key.Value] = true
		out = append(out, key, merge(val, u, ft))
	}
	for _, k := range updOrder {
		if !used[k] {
			out = append(out, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, updVals[k])
		}
	}
	orig.Content = out
	return orig
}

func mergeSequence(orig, upd *yaml.Node, elem reflect.Type) *yaml.Node {
	idKeys := identityKeys[deref(elem)]
	if idKeys == nil {
		if sameScalars(orig, upd) {
			return orig
		}
		return replace(orig, upd)
	}

	pool := map[string][]*yaml.Node{}
	for _, item := range orig.Content {
		if id := identity(item, idKeys); id != "" {
			pool[id] = append(pool[id], item)
		}
	}
	out := make([]*yaml.Node, 0, len(upd.Content))
	for _, item := range upd.Content {
		id := identity(item, idKeys)
		if cands := pool[id]; id != "" && len(cands) > 0 {
			pool[id] = cands[1:]
			out = append(out, merge(cands[0], item, elem))
			continue
		}
		out = append(out, item)
	}
	orig.Content = out
	return orig
}

// replace returns upd, carrying over comments and flow style of orig so a
// changed value does not lose what the hand editor wrote around it.
func replace(orig, upd *yaml.Node) *yaml.Node {
	if upd.HeadComment == "" {
		upd.HeadComment = orig.HeadComment
	}
	if upd.LineComment == "" {
		upd.LineComment = orig.LineComment
	}
	if upd.FootComment == "" {
		upd.FootComment = orig.FootComment
	}
	if orig.Kind == upd.Kind && upd.Kind != yaml.ScalarNode {
		upd.Style |= orig.Style & yaml.FlowStyle
	}
	return upd
}

func sameScalars(a, b *yaml.Node) bool {
	if len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		x, y := a.Content[i], b.Content[i]
		if x.Kind != yaml.ScalarNode || y.Kind != yaml.ScalarNode || x.Value != y.Value {
			return false
		}
	}
	return true
}

func identity(item *yaml.Node, keys []string) string {
	if item.Kind != yaml.MappingNode {
		return ""
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		v := lookup(item, k)
		if v == nil || v.Kind != yaml.ScalarNode {
			return ""
		}
		parts[i] = v.Value
	}
	return strings.Join(parts, "\x00")
}

// unknownKeys lists keys in n that type t does not know, as dotted paths.
func unknownKeys(n *yaml.Node, t reflect.Type, path string) []string {
	t = deref(t)
	var out []string
	switch {
	case t.Kind() == reflect.Struct && !isScalarType(t) && n.Kind == yaml.MappingNode:
		fields := fieldTypes(t)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			p := joinPath(path, k)
			if ft, ok := fields[k]; ok {
				out = append(out, unknownKeys(n.Content[i+1], ft, p)...)
			} else {
				out = append(out, fmt.Sprintf("unknown key %s (line %d) is kept", p, n.Content[i].Line))
			}
		}
	case t.Kind() == reflect.Slice && n.Kind == yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, unknownKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return out
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// fieldTypes maps the yaml key of every field of struct t to its type.
func fieldTypes(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		out[name] = f.Type
	}
	return out
}

func isScalarType(t reflect.Type) bool {
	t = deref(t)
	if t == timeType || t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) {
		return true
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Slice, reflect.Map, reflect.Array:
		return false
	}
	return true
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}
