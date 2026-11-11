// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// TestLFSOverHTTP: the batch API hands out the files a mirror keeps and
// takes uploads for own projects, only with matching content (T-9.18).
func TestLFSOverHTTP(t *testing.T) {
	e := setup(t)
	content := bytes.Repeat([]byte("modell"), 50000)
	oid, _ := gitcli.SHA256(bytes.NewReader(content))
	if err := gitcli.PutLFS(e.repo.Paths.Mirror, oid, int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	base := e.http.URL + "/git/" + e.repo.Entry.Key + ".git/info/lfs/objects"
	batch := func(op string, oids ...string) (int, []map[string]any) {
		t.Helper()
		var objs []map[string]any
		for _, o := range oids {
			objs = append(objs, map[string]any{"oid": o, "size": len(content)})
		}
		b, _ := json.Marshal(map[string]any{"operation": op, "transfers": []string{"basic"}, "objects": objs})
		req, _ := http.NewRequest(http.MethodPost, base+"/batch", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
		req.Header.Set("Accept", "application/vnd.git-lfs+json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Objects []map[string]any `json:"objects"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Objects
	}

	missing := strings.Repeat("0", 64)
	code, objs := batch("download", oid, missing)
	if code != 200 || len(objs) != 2 {
		t.Fatalf("batch = %d %v", code, objs)
	}
	href := objs[0]["actions"].(map[string]any)["download"].(map[string]any)["href"].(string)
	if objs[1]["error"] == nil {
		t.Error("missing object has no error")
	}
	resp, err := http.Get(href)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, content) {
		t.Errorf("download: %d bytes", len(got))
	}

	// Mirrors take no uploads.
	if code, _ := batch("upload", missing); code != http.StatusForbidden {
		t.Errorf("upload batch on mirror = %d", code)
	}

	// Own project: upload checked against its hash.
	own, err := e.lib.CreateLocal(context.Background(), "", "werkzeug", "", nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	put := func(oid string, body []byte) int {
		req, _ := http.NewRequest(http.MethodPut, e.http.URL+"/git/"+own.Entry.Key+".git/info/lfs/objects/"+oid, bytes.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := put(oid, []byte("falsch")); c != http.StatusUnprocessableEntity {
		t.Errorf("wrong content = %d", c)
	}
	if c := put(oid, content); c != http.StatusOK || !gitcli.HasLFS(own.Paths.Mirror, oid) {
		t.Errorf("upload = %d", c)
	}
	os.Chmod(gitcli.LFSObjectPath(own.Paths.Mirror, oid), 0o444)
	if c := put(oid, content); c != http.StatusOK { // again, onto a read-only object (R-007)
		t.Errorf("second upload = %d", c)
	}
	if c := put("../../etc", content); c == http.StatusOK {
		t.Error("bad oid accepted")
	}
}
