// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
)

// ---- README images kept with the repository (V-4, T-9.23, R-023) ----
//
// Images a README embeds from other servers are downloaded during the sync
// and served from RepoShelf: the archive stays complete when they vanish,
// and viewing a README no longer tells foreign servers one's address.
// The URLs come from foreign data: only http(s), never addresses of this
// machine or the home network (SSRF), images only, bounded in number and
// size.

// ReadmeAssetsDir holds the images next to repo.yaml.
const ReadmeAssetsDir = "readme-assets"

const (
	readmeAssetIndex = "index.json"
	maxReadmeImages  = 30
	maxReadmeImage   = 5 << 20
	readmeFetchTime  = 20 * time.Second
)

// ReadmeAsset is one stored image.
type ReadmeAsset struct {
	File    string    `json:"file"` // name below readme-assets/
	Type    string    `json:"type"`
	Size    int64     `json:"size"`
	Fetched time.Time `json:"fetched"`
}

// ErrPrivateAddress is returned for URLs that point into the local network.
var ErrPrivateAddress = errors.New("address in the local network refused")

// publicHTTP is a client that only connects to public addresses: the dial
// itself checks the resolved IP, so DNS tricks and redirects cannot reach
// the home network either.
var publicHTTP = &http.Client{
	Timeout: readmeFetchTime,
	Transport: &http.Transport{
		// No proxy: the address check must see the real destination.
		Proxy: nil,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
			}
			return nil
		}}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
			return errors.New("redirect refused")
		}
		return nil
	},
}

// readmeClient fetches README images (tests use a local server).
var readmeClient = publicHTTP

// publicIP reports whether ip is reachable on the internet (not loopback,
// private, link-local, multicast, unspecified or carrier-grade NAT).
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10
		return false
	}
	return true
}

// readmeImageURLs lists the absolute http(s) images of a Markdown text.
func readmeImageURLs(src []byte) []string {
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(src))
	var out []string
	seen := map[string]bool{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering {
			u := string(img.Destination)
			if (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) && !seen[u] && len(out) < maxReadmeImages {
				seen[u] = true
				out = append(out, u)
			}
		}
		return ast.WalkContinue, nil
	})
	return out
}

func (l *Library) readmeAssetDir(r *Repo) string {
	return filepath.Join(r.Paths.Meta, ReadmeAssetsDir)
}

// ReadmeAssets returns the stored images of a repository by URL.
func (l *Library) ReadmeAssets(r *Repo) map[string]ReadmeAsset {
	b, err := os.ReadFile(filepath.Join(l.readmeAssetDir(r), readmeAssetIndex))
	if err != nil {
		return nil
	}
	var idx map[string]ReadmeAsset
	if json.Unmarshal(b, &idx) != nil {
		return nil
	}
	return idx
}

// ReadmeAssetPath returns the file of a stored image; name must be one of
// the index (no paths from the request reach the file system).
func (l *Library) ReadmeAssetPath(ctx context.Context, ref, name string) (string, ReadmeAsset, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return "", ReadmeAsset{}, err
	}
	for _, a := range l.ReadmeAssets(r) {
		if a.File == name {
			return filepath.Join(l.readmeAssetDir(r), a.File), a, nil
		}
	}
	return "", ReadmeAsset{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// syncReadmeAssets downloads the README's images that are not stored yet.
// Images stay when the README drops them (the archive keeps history).
func (l *Library) syncReadmeAssets(ctx context.Context, r *Repo) error {
	if !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		return nil
	}
	_, readme, err := l.Git.Readme(ctx, r.Paths.Mirror, "HEAD", readmeLimit)
	if err != nil || len(readme) == 0 {
		return nil
	}
	urls := readmeImageURLs(readme)
	if len(urls) == 0 {
		return nil
	}
	idx := l.ReadmeAssets(r)
	if idx == nil {
		idx = map[string]ReadmeAsset{}
	}
	dir := l.readmeAssetDir(r)
	changed := false
	var errs []string
	for _, u := range urls {
		if _, ok := idx[u]; ok {
			continue
		}
		a, err := fetchReadmeImage(ctx, u, dir)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		idx[u] = a
		changed = true
	}
	if changed {
		b, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return err
		}
		if err := atomicfile.WriteFile(filepath.Join(dir, readmeAssetIndex), b, filePerm); err != nil {
			return err
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d README-Bilder nicht gesichert (z. B. %s)", len(errs), errs[0])
	}
	return nil
}

func fetchReadmeImage(ctx context.Context, raw, dir string) (ReadmeAsset, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ReadmeAsset{}, fmt.Errorf("%s: invalid URL", raw)
	}
	ctx, cancel := context.WithTimeout(ctx, readmeFetchTime)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ReadmeAsset{}, err
	}
	req.Header.Set("User-Agent", "RepoShelf")
	resp, err := readmeClient.Do(req)
	if err != nil {
		return ReadmeAsset{}, fmt.Errorf("%s: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReadmeAsset{}, fmt.Errorf("%s: HTTP %d", u.Host, resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
		"image/svg+xml": ".svg", "image/avif": ".avif", "image/x-icon": ".ico", "image/vnd.microsoft.icon": ".ico"}[mt]
	if ext == "" {
		return ReadmeAsset{}, fmt.Errorf("%s: kein Bild (%s)", u.Host, mt)
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return ReadmeAsset{}, err
	}
	tmp, err := os.CreateTemp(dir, ".img-*")
	if err != nil {
		return ReadmeAsset{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxReadmeImage+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return ReadmeAsset{}, err
	}
	if n > maxReadmeImage {
		return ReadmeAsset{}, fmt.Errorf("%s: größer als %d MB", u.Host, maxReadmeImage>>20)
	}
	name := hex.EncodeToString(h.Sum(nil))[:32] + ext
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return ReadmeAsset{}, err
	}
	return ReadmeAsset{File: name, Type: mt, Size: n, Fetched: time.Now().UTC().Truncate(time.Second)}, nil
}
