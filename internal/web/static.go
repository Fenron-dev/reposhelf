// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"sync"
)

// Cache rules of /static/ (R-031). Pages link the files with the version
// of the embedded files in the address, so a browser may keep them for
// good and still gets new ones right after an update. Addresses without
// that version (old pages, external-image.svg in READMEs) are checked with
// the server on every use, which costs a 304 at most.
const (
	cacheVersioned = "public, max-age=31536000, immutable"
	cacheCheck     = "no-cache"
	// versionLen hex digits of the content hash are plenty to tell
	// releases apart.
	versionLen = 12
)

// staticVersion is a hash over all embedded static files.
var staticVersion = sync.OnceValue(func() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(staticFS, p)
		if err != nil {
			return err
		}
		h.Write([]byte(p + "\x00"))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
})

// staticURL is the address of a static file for pages.
func staticURL(name string) string {
	return "/static/" + name + "?v=" + staticVersion()
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := staticVersion()
		hd := w.Header()
		if r.URL.Query().Get("v") == v {
			hd.Set("Cache-Control", cacheVersioned)
		} else {
			hd.Set("Cache-Control", cacheCheck)
		}
		// The file server answers If-None-Match with 304 against this tag.
		hd.Set("ETag", `"`+v+`"`)
		h.ServeHTTP(w, r)
	})
}
