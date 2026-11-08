// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/store"
)

// SmartFilter is a saved filter in the {logic, rules} format shared with
// MediaShelf and OracleVault (KONZEPT 6.6, T-2.4):
//
//	logic: and | or
//	rules: [{field, op, value}, …]
type SmartFilter struct {
	Logic string      `yaml:"logic" json:"logic"`
	Rules []SmartRule `yaml:"rules" json:"rules"`
}

// SmartRule is one condition.
type SmartRule struct {
	Field string `yaml:"field" json:"field"`
	Op    string `yaml:"op" json:"op"`
	Value any    `yaml:"value" json:"value"`
}

// SmartFields lists the fields a rule may test, with their type.
var SmartFields = map[string]string{
	"kind": "text", "status": "text", "host": "text", "owner": "text", "name": "text",
	"description": "text", "language": "text", "license": "text", "tag": "list",
	"collection": "list", "topic": "list", "buildable": "bool", "stars": "number",
	"size_mb": "number", "releases": "number", "versions": "number",
	"created": "date", "last_success": "date", "pushed_at": "date", "text": "text",
}

// SmartOps lists the operators (MediaShelf names).
var SmartOps = []string{"equals", "not_equals", "contains", "not_contains", "starts_with",
	"gt", "gte", "lt", "lte", "between", "in", "is_empty", "is_not_empty", "older_than_days", "newer_than_days"}

// Validate checks fields and operators.
func (f SmartFilter) Validate() error {
	switch strings.ToLower(f.Logic) {
	case "and", "or", "":
	default:
		return fmt.Errorf("logic must be and or or, is %q", f.Logic)
	}
	for i, r := range f.Rules {
		if _, ok := SmartFields[r.Field]; !ok {
			return fmt.Errorf("rules[%d]: unknown field %q", i, r.Field)
		}
		if !slices.Contains(SmartOps, r.Op) {
			return fmt.Errorf("rules[%d]: unknown op %q", i, r.Op)
		}
	}
	return nil
}

// SmartMembers returns the repositories matching f.
func (l *Library) SmartMembers(ctx context.Context, f SmartFilter) ([]store.Entry, error) {
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	all, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return nil, err
	}
	var textHits map[string]bool
	for _, r := range f.Rules {
		if r.Field == "text" {
			textHits = map[string]bool{}
			hits, err := l.Store.List(ctx, store.Query{Text: fmt.Sprint(r.Value)})
			if err != nil {
				return nil, err
			}
			for _, h := range hits {
				textHits[h.ID] = true
			}
		}
	}
	now := l.now()
	var out []store.Entry
	for _, e := range all {
		if f.Match(e, now, textHits) {
			out = append(out, e)
		}
	}
	return out, nil
}

// Match evaluates f for one entry. An empty rule list matches nothing, so
// a half-configured smart collection does not show the whole library.
func (f SmartFilter) Match(e store.Entry, now time.Time, textHits map[string]bool) bool {
	if len(f.Rules) == 0 {
		return false
	}
	or := strings.EqualFold(f.Logic, "or")
	for _, r := range f.Rules {
		ok := r.match(e, now, textHits)
		if or && ok {
			return true
		}
		if !or && !ok {
			return false
		}
	}
	return !or
}

func (r SmartRule) match(e store.Entry, now time.Time, textHits map[string]bool) bool {
	switch SmartFields[r.Field] {
	case "list":
		var list []string
		switch r.Field {
		case "tag":
			list = e.Tags
		case "collection":
			list = e.Collections
		case "topic":
			list = e.Topics
		}
		return matchList(list, r.Op, r.Value)
	case "bool":
		want := fmt.Sprint(r.Value) == "true" || r.Value == true
		if r.Op == "not_equals" {
			return e.Buildable != want
		}
		return e.Buildable == want
	case "number":
		var v float64
		switch r.Field {
		case "stars":
			v = float64(e.Stars)
		case "size_mb":
			v = float64(e.SizeBytes) / (1 << 20)
		case "releases":
			v = float64(e.Releases)
		case "versions":
			v = float64(e.Versions)
		}
		return matchNumber(v, r.Op, r.Value)
	case "date":
		var t *time.Time
		switch r.Field {
		case "created":
			t = &e.Created
		case "last_success":
			t = e.LastSuccess
		case "pushed_at":
			t = e.PushedAt
		}
		return matchDate(t, now, r.Op, r.Value)
	}
	if r.Field == "text" {
		return textHits[e.ID] == (r.Op != "not_contains")
	}
	var s string
	switch r.Field {
	case "kind":
		s = e.Kind
	case "status":
		s = e.Status
	case "host":
		s = e.Host
	case "owner":
		s = e.Owner
	case "name":
		s = e.Name
	case "description":
		s = e.Description
	case "language":
		s = e.Language
	case "license":
		s = e.License
	}
	return matchText(s, r.Op, r.Value)
}

func values(v any) []string {
	switch t := v.(type) {
	case []any:
		var out []string
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	case []string:
		return t
	case nil:
		return nil
	default:
		return strings.Split(fmt.Sprint(t), ",")
	}
}

func matchText(s, op string, v any) bool {
	want := fmt.Sprint(v)
	ls, lw := strings.ToLower(s), strings.ToLower(want)
	switch op {
	case "equals":
		return ls == lw
	case "not_equals":
		return ls != lw
	case "contains":
		return strings.Contains(ls, lw)
	case "not_contains":
		return !strings.Contains(ls, lw)
	case "starts_with":
		return strings.HasPrefix(ls, lw)
	case "in":
		return slices.ContainsFunc(values(v), func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), s) })
	case "is_empty":
		return s == ""
	case "is_not_empty":
		return s != ""
	}
	return false
}

// matchList: "contains"/"equals" also match sub-tags with a "/*" value.
func matchList(list []string, op string, v any) bool {
	has := func(want string) bool {
		want = strings.TrimSpace(want)
		prefix, sub := strings.CutSuffix(want, "/*")
		return slices.ContainsFunc(list, func(x string) bool {
			return strings.EqualFold(x, want) || sub && (strings.EqualFold(x, prefix) || strings.HasPrefix(strings.ToLower(x), strings.ToLower(prefix)+"/"))
		})
	}
	switch op {
	case "contains", "equals":
		return has(fmt.Sprint(v))
	case "not_contains", "not_equals":
		return !has(fmt.Sprint(v))
	case "in":
		return slices.ContainsFunc(values(v), has)
	case "is_empty":
		return len(list) == 0
	case "is_not_empty":
		return len(list) > 0
	}
	return false
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case int:
		return float64(t), nil
	case float64:
		return t, nil
	default:
		return strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(v)), 64)
	}
}

func matchNumber(x float64, op string, v any) bool {
	if op == "between" {
		vs := values(v)
		if len(vs) != 2 {
			return false
		}
		lo, err1 := toFloat(vs[0])
		hi, err2 := toFloat(vs[1])
		return err1 == nil && err2 == nil && x >= lo && x <= hi
	}
	want, err := toFloat(v)
	if err != nil {
		return false
	}
	switch op {
	case "equals":
		return x == want
	case "not_equals":
		return x != want
	case "gt":
		return x > want
	case "gte":
		return x >= want
	case "lt":
		return x < want
	case "lte":
		return x <= want
	}
	return false
}

var errNoDate = errors.New("no date")

func matchDate(t *time.Time, now time.Time, op string, v any) bool {
	switch op {
	case "is_empty":
		return t == nil || t.IsZero()
	case "is_not_empty":
		return t != nil && !t.IsZero()
	}
	if t == nil || t.IsZero() {
		return false
	}
	switch op {
	case "older_than_days", "newer_than_days":
		days, err := toFloat(v)
		if err != nil {
			return false
		}
		age := now.Sub(*t).Hours() / 24
		if op == "older_than_days" {
			return age > days
		}
		return age <= days
	}
	want, err := parseDate(v)
	if err != nil {
		return false
	}
	switch op {
	case "gt":
		return t.After(want)
	case "gte":
		return !t.Before(want)
	case "lt":
		return t.Before(want)
	case "lte":
		return !t.After(want)
	case "equals":
		return t.Format(time.DateOnly) == want.Format(time.DateOnly)
	}
	return false
}

func parseDate(v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errNoDate
}
