package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The cabinet is served by this binary, from this binary.
//
// It ships as source — React, ReactDOM and htm are vendored files, the screens
// are plain ES modules, and nothing is transpiled — so what is embedded here is
// what a developer edits. There is no build output to regenerate, no bundler in
// CI, and no way for the served cabinet to drift from the repository.
//
// Being same-origin with the API is not an accident either: the session cookie
// is HttpOnly and SameSite=Lax, so a cabinet on another origin would need CORS
// credentials and a token in JavaScript's reach. Here it needs neither.
//
//go:embed all:webui
var webuiFS embed.FS

// cabinetAsset is one embedded file, with the bytes and the two headers
// serving it needs, computed once at startup rather than per request.
type cabinetAsset struct {
	body        []byte
	contentType string
	etag        string
}

// apiPrefixes are the paths that belong to the API. The cabinet handler is
// reached as the router's NotFound, so it must refuse to answer for them:
// a client that mistypes an API path deserves the API's JSON 404, not a page.
var apiPrefixes = []string{"/api/", "/agent/", "/health"}

type cabinetHandler struct {
	assets map[string]cabinetAsset
	index  cabinetAsset
}

func newCabinetHandler() (*cabinetHandler, error) {
	root, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		return nil, err
	}
	h := &cabinetHandler{assets: map[string]cabinetAsset{}}
	err = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		asset := cabinetAsset{
			body:        body,
			contentType: contentTypeFor(p),
			// A strong ETag over the bytes: the files are not
			// content-hashed in their names, so this is what keeps a
			// reload cheap without ever serving a stale cabinet.
			etag: `"` + hex.EncodeToString(sum[:16]) + `"`,
		}
		h.assets["/"+p] = asset
		if p == "index.html" {
			h.index = asset
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return h, nil
}

// contentTypeFor keeps the content types explicit. mime.TypeByExtension reads
// the host's /etc/mime.types, which is empty in a scratch container and has
// been known to call .js something a browser refuses to execute as a module.
func contentTypeFor(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".map":
		return "application/json"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".png":
		return "image/png"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func (h *cabinetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}
	for _, prefix := range apiPrefixes {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix) {
			writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
			return
		}
	}

	clean := path.Clean("/" + r.URL.Path)
	asset, ok := h.assets[clean]
	if !ok {
		// Every other path is the cabinet's own: it routes on the hash, but a
		// deep link, a refresh on /settings, or a proxy's idea of a directory
		// must all land on the page rather than on a 404.
		asset = h.index
	}
	if asset.body == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "Not found"})
		return
	}

	// The cabinet loads nothing from anywhere else — that is what vendoring
	// React bought — so the policy can say exactly that, and an injected
	// <script src> or an inline handler has nowhere to run.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("ETag", asset.etag)
	// Not immutable: the filenames carry no content hash, so a cached copy
	// must be revalidated or a customer keeps yesterday's cabinet after an
	// upgrade. The ETag makes that revalidation a 304 with no body.
	w.Header().Set("Cache-Control", "no-cache")

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, asset.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(asset.body); err != nil {
		slog.Debug("cabinet write interrupted", "path", clean, "error", err)
	}
}
