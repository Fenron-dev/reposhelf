// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"html/template"
	"net/http"
)

// FollowRedirects turns HTTP redirects into a small page that navigates by
// itself. macOS' WKWebView does not follow redirects answered through
// Wails' custom URL scheme, so after "save" or "add" the window would stay
// on the form (reported for v0.1.5).
func FollowRedirects(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&redirectWriter{ResponseWriter: w}, r)
	})
}

type redirectWriter struct {
	http.ResponseWriter
	redirected bool
}

func (rw *redirectWriter) WriteHeader(code int) {
	loc := rw.Header().Get("Location")
	if code >= 300 && code < 400 && loc != "" {
		rw.redirected = true
		h := rw.Header()
		h.Del("Location")
		h.Del("Content-Length")
		h.Set("Content-Type", "text/html; charset=utf-8")
		rw.ResponseWriter.WriteHeader(http.StatusOK)
		_ = redirectPage.Execute(rw.ResponseWriter, loc)
		return
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *redirectWriter) Write(p []byte) (int, error) {
	if rw.redirected {
		return len(p), nil // drop the original "See Other" body
	}
	return rw.ResponseWriter.Write(p)
}

// Flush keeps streaming responses working.
func (rw *redirectWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var redirectPage = template.Must(template.New("r").Parse(`<!doctype html><html lang="de"><head><meta charset="utf-8">` +
	`<meta http-equiv="refresh" content="0;url={{.}}"><title>RepoShelf</title>` +
	`<style>body{background:#0E1014;color:#9BA3AF;font:14px system-ui;padding:24px}a{color:#F2B544}</style></head>` +
	`<body>Weiter … <a href="{{.}}">falls nichts passiert, hier klicken</a></body></html>`))
