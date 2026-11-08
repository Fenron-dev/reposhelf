// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// DefinitionFile is the simple build definition in a repository
// (KONZEPT 6.12, T-7.3).
const DefinitionFile = "reposhelf-build.yaml"

// Definition is the content of reposhelf-build.yaml:
//
//	targets:
//	  - name: linux
//	    image: golang:1.24
//	    commands: ["go build -o dist/app ./cmd/app"]
//	    artifacts: ["dist/*"]
//	    env: {CGO_ENABLED: "0"}
//	    network: true          # false = offline
//	    secrets: [NPM_TOKEN]   # passed from REPOSHELF_SECRET_NPM_TOKEN
//	    needs: [linux/amd64]   # runner capabilities
type Definition struct {
	Targets []Target `yaml:"targets"`
}

// Target is one buildable thing.
type Target struct {
	Name      string            `yaml:"name"`
	Image     string            `yaml:"image"`
	Commands  []string          `yaml:"commands"`
	Artifacts []string          `yaml:"artifacts"`
	Env       map[string]string `yaml:"env"`
	Network   *bool             `yaml:"network"`
	Secrets   []string          `yaml:"secrets"`
	Needs     []string          `yaml:"needs"`
	// Kind is derived: docker, act (workflow job) or github (dispatch).
	Kind     string `yaml:"-"`
	Workflow string `yaml:"-"`
	Job      string `yaml:"-"`
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,63}$`)

// Validate checks a definition.
func (d Definition) Validate() error {
	seen := map[string]bool{}
	for i, t := range d.Targets {
		switch {
		case !namePattern.MatchString(t.Name):
			return fmt.Errorf("targets[%d]: invalid name %q", i, t.Name)
		case seen[t.Name]:
			return fmt.Errorf("targets[%d]: duplicate name %q", i, t.Name)
		case t.Image == "":
			return fmt.Errorf("targets[%d]: image missing", i)
		case len(t.Commands) == 0:
			return fmt.Errorf("targets[%d]: commands missing", i)
		}
		for _, a := range t.Artifacts {
			if strings.HasPrefix(a, "/") || strings.Contains(a, "..") {
				return fmt.Errorf("targets[%d]: artifact pattern %q must stay inside the source", i, a)
			}
		}
		seen[t.Name] = true
	}
	return nil
}

// Targets lists everything that can be built at commit: targets from
// reposhelf-build.yaml, jobs of .github/workflows (run with act) and, for
// GitHub repositories, workflows that GitHub Actions can run (dispatch).
func Targets(ctx context.Context, g *gitcli.Git, mirror, commit string, onGitHub bool) ([]Target, error) {
	var out []Target
	if b, err := g.ReadFile(ctx, mirror, commit, DefinitionFile, 1<<20); err == nil {
		var d Definition
		if err := yaml.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("%s: %w", DefinitionFile, err)
		}
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", DefinitionFile, err)
		}
		for _, t := range d.Targets {
			t.Kind = "docker"
			out = append(out, t)
		}
	}
	entries, err := g.ListTree(ctx, mirror, commit, ".github/workflows")
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		if e.Type != "blob" || (!strings.HasSuffix(e.Name, ".yml") && !strings.HasSuffix(e.Name, ".yaml")) {
			continue
		}
		b, err := g.ReadFile(ctx, mirror, commit, e.Path, 1<<20)
		if err != nil {
			continue
		}
		var wf struct {
			On   any                       `yaml:"on"`
			Jobs map[string]map[string]any `yaml:"jobs"`
		}
		if yaml.Unmarshal(b, &wf) != nil {
			continue
		}
		for job, def := range wf.Jobs {
			runsOn := fmt.Sprint(def["runs-on"])
			if strings.Contains(runsOn, "ubuntu") || strings.Contains(runsOn, "linux") {
				out = append(out, Target{Name: "act:" + strings.TrimSuffix(e.Name, path.Ext(e.Name)) + "/" + job, Kind: "act", Workflow: e.Path, Job: job})
			}
		}
		if onGitHub && hasDispatch(wf.On) {
			out = append(out, Target{Name: "github:" + e.Name, Kind: "github", Workflow: e.Name})
		}
	}
	return out, nil
}

func hasDispatch(on any) bool {
	switch v := on.(type) {
	case string:
		return v == "workflow_dispatch"
	case []any:
		for _, x := range v {
			if fmt.Sprint(x) == "workflow_dispatch" {
				return true
			}
		}
	case map[string]any:
		_, ok := v["workflow_dispatch"]
		return ok
	}
	return false
}
