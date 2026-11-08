// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// migration rewrites the YAML tree of schema N into schema N+1. It works on
// the tree, not on Repo, so unknown keys and comments survive migrations too.
type migration func(root *yaml.Node) error

// migrations maps the source schema to the step that lifts it by one.
// Schema 1 is the first version, so there is nothing to migrate yet. Every
// new entry needs a fixture of the old version in testdata/.
var migrations = map[int]migration{}

func migrate(root *yaml.Node, from int) error {
	for v := from; v < CurrentSchema; v++ {
		step, ok := migrations[v]
		if !ok {
			return fmt.Errorf("no migration from schema %d to %d", v, v+1)
		}
		if err := step(root); err != nil {
			return fmt.Errorf("migrate schema %d to %d: %w", v, v+1, err)
		}
		if n := lookup(root, "schema"); n != nil {
			n.Value = strconv.Itoa(v + 1)
		}
	}
	return nil
}
