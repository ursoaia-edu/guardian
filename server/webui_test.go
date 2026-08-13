package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cabinetRouter(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newCabinetHandler()
	if err != nil {
		t.Fatalf("build the cabinet handler: %v", err)
	}
	s := &Server{cabinet: handler}
	return s.setupRoutes()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr
}

func TestCabinetServesTheIndexAtTheRoot(t *testing.T) {
	rr := get(t, cabinetRouter(t), "/")
	if rr.Code != 200 {
		t.Fatalf("GET /: %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Error("the page has no mount point for React")
	}
	// The vendored runtime is what makes this cabinet work with no CDN and no
	// build step; a page that lost the script tags would render nothing.
	for _, want := range []string{"/vendor/react.production.min.js", "/vendor/react-dom.production.min.js", "/app.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html does not load %s", want)
		}
	}
}

// Every screen is a deep link somebody can bookmark or refresh on. They must
// all land on the page, not on a 404 from the router.
func TestCabinetDeepLinksFallBackToTheIndex(t *testing.T) {
	h := cabinetRouter(t)
	for _, path := range []string{"/settings", "/rooms/some-id/rules", "/computers/abc"} {
		rr := get(t, h, path)
		if rr.Code != 200 {
			t.Errorf("GET %s: %d, want the cabinet", path, rr.Code)
			continue
		}
		if !strings.Contains(rr.Body.String(), `id="app"`) {
			t.Errorf("GET %s did not serve the cabinet page", path)
		}
	}
}

// The modules are loaded with <script type="module">, and a browser refuses to
// execute one served as anything but a JavaScript type. Getting this wrong
// gives a blank page and a console error, with nothing wrong on the server.
func TestCabinetServesModulesAsJavaScript(t *testing.T) {
	h := cabinetRouter(t)
	cases := map[string]string{
		"/app.js":                         "text/javascript",
		"/screens/room.js":                "text/javascript",
		"/vendor/react.production.min.js": "text/javascript",
		"/app.css":                        "text/css",
	}
	for path, wantType := range cases {
		rr := get(t, h, path)
		if rr.Code != 200 {
			t.Errorf("GET %s: %d", path, rr.Code)
			continue
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("GET %s: Content-Type = %q, want %s", path, ct, wantType)
		}
		if rr.Body.Len() == 0 {
			t.Errorf("GET %s served an empty file", path)
		}
	}
}

// The SPA fallback must not swallow the API. A mistyped API path answers the
// API's own JSON 404 — a client parsing an HTML page as JSON is a far more
// confusing failure than a 404.
func TestCabinetDoesNotAnswerForTheAPI(t *testing.T) {
	h := cabinetRouter(t)
	for _, path := range []string{"/api/v1/nope", "/api/v1/rooms/../x", "/agent/nope", "/health/nope"} {
		rr := get(t, h, path)
		if rr.Code != 404 {
			t.Errorf("GET %s: %d, want 404", path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "<!doctype html") {
			t.Errorf("GET %s served the cabinet page instead of a JSON error", path)
		}
	}
}

// A signed-in cabinet polls every few seconds and reloads often; without a
// validator each reload would re-download every asset.
func TestCabinetRevalidatesWithAnETag(t *testing.T) {
	h := cabinetRouter(t)
	first := get(t, h, "/app.js")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on an asset")
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q; an asset with no content hash in its name must be revalidated", cc)
	}

	req := httptest.NewRequest("GET", "/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("a matching If-None-Match returned %d, want 304", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Error("a 304 carried a body")
	}
}

// The cabinet loads nothing from any other origin, which is exactly what the
// policy should say: an injected script tag then has nowhere to load from.
func TestCabinetSendsItsSecurityHeaders(t *testing.T) {
	rr := get(t, cabinetRouter(t), "/")
	csp := rr.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP loosened to %q; the vendored React needs neither", csp)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options: nosniff")
	}
}

func TestCabinetRefusesWritesAndServesHEAD(t *testing.T) {
	h := cabinetRouter(t)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/settings", nil))
	if rr.Code != 404 {
		t.Errorf("POST to a cabinet path: %d, want 404", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("HEAD", "/app.css", nil))
	if rr.Code != 200 {
		t.Fatalf("HEAD /app.css: %d", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Error("HEAD returned a body")
	}
	if rr.Header().Get("Content-Type") == "" {
		t.Error("HEAD returned no Content-Type")
	}
}

// Every module the cabinet imports must actually be embedded: a screen added
// to the directory but left out of the binary is a blank page that only shows
// up in a browser.
func TestEveryCabinetModuleIsEmbedded(t *testing.T) {
	handler, err := newCabinetHandler()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	required := []string{
		"/index.html", "/app.css", "/app.js", "/api.js", "/router.js", "/format.js",
		"/hooks.js", "/ui.js", "/favicon.svg",
		"/screens/signin.js", "/screens/welcome.js", "/screens/overview.js",
		"/screens/rooms.js", "/screens/room.js", "/screens/computers.js",
		"/screens/computer.js", "/screens/install.js", "/screens/activity.js",
		"/screens/settings.js",
		"/vendor/react.production.min.js", "/vendor/react-dom.production.min.js", "/vendor/htm.js",
	}
	for _, path := range required {
		if _, ok := handler.assets[path]; !ok {
			t.Errorf("%s is not embedded in the binary", path)
		}
	}
}
