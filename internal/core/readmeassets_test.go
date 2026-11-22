// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func TestPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.5": false, "192.168.1.10": false, "172.16.0.1": false, "169.254.169.254": false,
		"100.64.1.1": false, "::1": false, "fe80::1": false, "fd00::1": false, "0.0.0.0": false, "224.0.0.1": false,
	} {
		if got := publicIP(net.ParseIP(ip)); got != want {
			t.Errorf("publicIP(%s) = %v", ip, got)
		}
	}
}

// TestReadmeImages: images of the README are kept during the sync; the
// home network is out of reach, only images within the size limit (V-4).
func TestReadmeImages(t *testing.T) {
	ctx := context.Background()
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 100)...)
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>"))
		case "/huge.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(bytes.Repeat([]byte{0}, maxReadmeImage+10))
		}
	}))
	defer img.Close()

	f := testutil.NewForge(t)
	up := f.NewRepo("o", "tool")
	up.Commit("README.md", "# Tool\n\n![Logo]("+img.URL+"/logo.png) ![x]("+img.URL+"/page) ![y]("+img.URL+"/huge.png) ![lokal](docs/a.png)\n", "readme")
	up.Push()
	l, _ := openLib(t, f)

	// The real client refuses the local test server: no requests into
	// the home network from foreign READMEs.
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "tool"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.ReadmeAssets(r)) != 0 {
		t.Fatal("image fetched from a local address")
	}
	if _, err := fetchReadmeImage(ctx, img.URL+"/logo.png", t.TempDir()); !errors.Is(err, ErrPrivateAddress) {
		t.Errorf("local address = %v", err)
	}

	old := readmeClient
	readmeClient = img.Client()
	defer func() { readmeClient = old }()
	if err := l.syncReadmeAssets(ctx, r); err == nil || !strings.Contains(err.Error(), "2 README-Bilder") {
		t.Errorf("sync = %v", err)
	}
	assets := l.ReadmeAssets(r)
	a, ok := assets[img.URL+"/logo.png"]
	if !ok || len(assets) != 1 || a.Type != "image/png" || a.Size != int64(len(png)) {
		t.Fatalf("assets = %+v", assets)
	}
	p, _, err := l.ReadmeAssetPath(ctx, r.R().ID, a.File)
	if b, _ := os.ReadFile(p); err != nil || !bytes.Equal(b, png) {
		t.Errorf("stored file: %v", err)
	}
	if _, _, err := l.ReadmeAssetPath(ctx, r.R().ID, "../repo.yaml"); err == nil {
		t.Error("path outside the index served")
	}
}

// TestReadmeImageURLsHTML: images in HTML tags are kept too, each once
// and only absolute http(s) ones (R-029).
func TestReadmeImageURLsHTML(t *testing.T) {
	src := "<p align=\"center\"><img src=\"images/logo.png\"/></p>\n\n" +
		"<div><a href=\"https://s.example/go\"><img src=\"https://s.example/banner\" width=\"100%\"></a></div>\n\n" +
		"![x](https://s.example/banner) ![y](https://i.example/y.png) <img src=\"javascript:x\">\n"
	got := readmeImageURLs([]byte(src))
	want := []string{"https://s.example/banner", "https://i.example/y.png"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("urls = %v, want %v", got, want)
	}
}
