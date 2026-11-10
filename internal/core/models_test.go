// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func TestModelIDs(t *testing.T) {
	got := modelIDs([]string{
		`model = AutoModel.from_pretrained("hexgrad/Kokoro-82M")`,
		`path = hf_hub_download(repo_id='openai/whisper-small', filename="config.json")`,
		`snapshot_download("models/voice.onnx")`,
		`# see https://huggingface.co/datasets/a/b and https://huggingface.co/BAAI/bge-m3/tree/main`,
		`from_pretrained("hexgrad/Kokoro-82M")`,
	})
	want := []string{"BAAI/bge-m3", "hexgrad/Kokoro-82M", "openai/whisper-small"}
	if !slices.Equal(got, want) {
		t.Errorf("modelIDs = %v, want %v", got, want)
	}
}

func TestDetectModelsOnSync(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("me", "tts")
	up.Commit("app.py", "pipe = KPipeline(repo_id=\"hexgrad/Kokoro-82M\")\n", "app")
	up.Push()
	l, _ := openLib(t, f)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("me", "tts"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	r, _ = l.Get(ctx, r.R().ID)
	models := l.ModelsOf(ctx, r)
	if len(models) != 1 || models[0].ID != "hexgrad/Kokoro-82M" || models[0].RepoID != "" || models[0].URL != "https://huggingface.co/hexgrad/Kokoro-82M" {
		t.Errorf("models = %+v", models)
	}
}

// TestLargeModelNeedsConfirmation: models above retention.ask_above_mb are
// only added with a confirmation (D-025); LFS is switched on.
func TestLargeModelNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/org/big":
			w.Write([]byte(`{"id":"org/big","pipeline_tag":"text-to-speech"}`))
		case "/api/models/org/big/tree/main":
			w.Write([]byte(`[{"type":"file","path":"model.safetensors","size":134,"lfs":{"size":3000000000}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	f := testutil.NewForge(t)
	l, err := Open(ctx, Options{DataDir: t.TempDir(), Git: f.Git, NoKeychain: true,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceBaseURLs: map[string]string{"huggingface.co": api.URL + "/api"}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	pv, err := l.Preview(ctx, "https://huggingface.co/org/big")
	if err != nil || !pv.Large || pv.ModelBytes != 3000000000 {
		t.Fatalf("preview = %+v, %v", pv, err)
	}
	if _, err := l.Add(ctx, AddRequest{URL: "https://huggingface.co/org/big"}); !errors.Is(err, ErrTooLarge) && l.lfs {
		t.Errorf("unconfirmed large model = %v", err)
	}
	if !l.lfs {
		t.Skip("git-lfs not installed")
	}
	r, err := l.Add(ctx, AddRequest{URL: "https://huggingface.co/org/big", ConfirmLarge: true, Mode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if inc := r.R().Include; !inc.LFS || inc.Releases || inc.Issues {
		t.Errorf("model include = %+v", inc)
	}
}
