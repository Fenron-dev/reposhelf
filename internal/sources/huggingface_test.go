// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHuggingFace(t *testing.T) {
	for in, want := range map[string]string{
		"https://huggingface.co/hexgrad/Kokoro-82M":                       "https://huggingface.co/hexgrad/Kokoro-82M",
		"huggingface.co/hexgrad/Kokoro-82M/tree/main":                     "https://huggingface.co/hexgrad/Kokoro-82M",
		"https://huggingface.co/hexgrad/Kokoro-82M/blob/main/config.json": "https://huggingface.co/hexgrad/Kokoro-82M",
	} {
		p, err := ParseURL(in)
		if err != nil || p.Source.URL != want || p.Provider != KindHuggingFace || p.Source.Key() != "huggingface.co/hexgrad/Kokoro-82M" {
			t.Errorf("ParseURL(%q) = %+v, %v", in, p, err)
		}
	}
	if _, err := ParseURL("https://huggingface.co/datasets/a/b"); err == nil {
		t.Error("dataset accepted")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/models/org/tts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"org/tts","pipeline_tag":"text-to-speech","library_name":"pytorch","likes":7,
			"tags":["tts","license:apache-2.0","en"],"cardData":{"license":"apache-2.0"},"gated":false}`))
	})
	mux.HandleFunc("/api/models/org/tts/tree/main", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recursive") != "true" {
			t.Error("tree not recursive")
		}
		w.Write([]byte(`[{"type":"file","path":"config.json","size":120},
			{"type":"directory","path":"voices","size":0},
			{"type":"file","path":"model.safetensors","size":134,"lfs":{"size":2000000000}}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	reg := &Registry{BaseURLs: map[string]string{HuggingFaceHost: srv.URL + "/api"}}
	api, err := reg.API(HuggingFaceHost)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := ParseURL("https://huggingface.co/org/tts")
	m, err := api.Metadata(context.Background(), p.Source)
	if err != nil {
		t.Fatal(err)
	}
	if m.Size != 2000000120 || m.License != "apache-2.0" || m.Description != "Modell · text-to-speech · pytorch" || len(m.Topics) != 2 {
		t.Errorf("metadata = %+v", m)
	}
}
