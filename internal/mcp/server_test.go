// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func connect(t *testing.T, lib *core.Library, rights []string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := New(lib, Options{Rights: rights, Client: "test", CloneBase: func() string { return "http://shelf:7373" }})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
	}
	return out, res.IsError
}

func TestTools(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("o", "tool")
	up.Commit("src/main.go", "package main\n", "code")
	up.Push()
	lib, err := core.Open(ctx, core.Options{DataDir: t.TempDir(), Git: f.Git, NoKeychain: true,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceKinds:    map[string]string{f.Host: sources.KindGitHub},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()}})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if _, err := lib.Add(ctx, core.AddRequest{URL: f.URL("o", "tool"), Tags: []string{"dev/tool"}, Wait: true}); err != nil {
		t.Fatal(err)
	}

	// Read-only client sees only read tools – and never delete tools.
	ro := connect(t, lib, []string{auth.Read})
	tools, err := ro.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if slices.Contains(names, "add_repo") || !slices.Contains(names, "read_file") {
		t.Errorf("read-only tools = %v", names)
	}
	for _, n := range names {
		if strings.Contains(n, "delete") || strings.Contains(n, "trash") || strings.Contains(n, "purge") {
			t.Errorf("destructive tool exposed: %s", n)
		}
	}

	out, isErr := call(t, ro, "search_repos", map[string]any{"tag": "dev/*"})
	if isErr || len(out["repos"].([]any)) != 1 {
		t.Fatalf("search = %v", out)
	}
	out, _ = call(t, ro, "read_file", map[string]any{"repo": "o/tool", "path": "src/main.go"})
	if out["content"] != "package main\n" || out["untrusted_content"] != true {
		t.Errorf("read_file = %v", out)
	}
	out, _ = call(t, ro, "list_tree", map[string]any{"repo": "o/tool"})
	if len(out["entries"].([]any)) != 2 {
		t.Errorf("list_tree = %v", out)
	}
	out, _ = call(t, ro, "search_code", map[string]any{"pattern": "package main"})
	if hits, _ := out["hits"].([]any); len(hits) != 1 {
		t.Errorf("search_code = %v", out)
	}
	out, _ = call(t, ro, "get_clone_url", map[string]any{"repo": "o/tool"})
	if !strings.HasPrefix(out["url"].(string), "http://shelf:7373/git/") {
		t.Errorf("clone url = %v", out)
	}
	if _, isErr := call(t, ro, "read_file", map[string]any{"repo": "o/tool", "path": "x", "ref": "--upload-pack=evil"}); !isErr {
		t.Error("option injection in ref accepted")
	}

	// Writer adds relations only as suggestions.
	rw := connect(t, lib, []string{auth.Read, auth.Write})
	if _, isErr := call(t, rw, "add_relation", map[string]any{"repo": "o/tool", "type": "related", "target": "github.com/x/y"}); isErr {
		t.Fatal("add_relation failed")
	}
	r, _ := lib.Get(ctx, "o/tool")
	if r.R().Relations[0].State != "suggested" {
		t.Errorf("agent relation state = %s", r.R().Relations[0].State)
	}
	if _, isErr := call(t, rw, "write_notes", map[string]any{"repo": "o/tool", "text": "# Notiz"}); isErr {
		t.Error("write_notes failed")
	}
	if acts := lib.RecentActivity(20, ""); !slices.ContainsFunc(acts, func(a core.Activity) bool { return a.Actor == "mcp:test" }) {
		t.Error("MCP actions not logged")
	}
}
