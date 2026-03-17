# ProcSentinel Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remediate the findings in `specs/audit-2026-06-12.md` — eliminate fail-open auth and enforcement paths, make the agent unable to brick hosts, harden deployment, and bootstrap an automated test suite.

**Architecture:** The server stays a single Go binary (chi + modernc.org/sqlite) with in-memory caches; we add startup token validation, default-deny sync semantics, body/rate limits, and optional TLS. The agent's matching logic is extracted into pure, testable functions and the shared state pointer becomes `atomic.Pointer`. Deployment moves to a non-root systemd unit with generated tokens.

**Tech Stack:** Go 1.25 (`go-chi/chi`, `go-chi/httprate`, `modernc.org/sqlite`), Flutter (`flutter_secure_storage`), systemd, Docker (alpine).

**Conventions for all tasks:**
- Server commands run from `/Users/poligon/Workspace/guardian/server`, agent commands from `/Users/poligon/Workspace/guardian/agent`, mobile from `/Users/poligon/Workspace/guardian/mobile`.
- Server tests: `go test ./...` — agent tests: `go test ./...` (agent's Windows files are excluded by build tags on macOS/Linux; additionally typecheck with `GOOS=windows go build ./...` after touching `service_windows.go`; agent normally builds with `CGO_ENABLED=1` but plain `go build` suffices for typechecking).
- Audit finding IDs (SEC-*, BUG-*, IMP-*) refer to `specs/audit-2026-06-12.md`.
- Spec updates for all code changes are batched in Task 19 (repo rule from CLAUDE.md).

---

## Phase 0 — Test harness

### Task 1: Testable server constructor + httptest harness (IMP-01)

`NewServer` currently hardcodes `./guardian.db`, making tests impossible. Parameterize it and build the shared test helpers every later task uses.

**Files:**
- Modify: `server/main.go:29-30,66`
- Create: `server/server_test.go`

- [ ] **Step 1: Write the failing test + helpers**

Create `server/server_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const (
	testClientToken = "test-client-token-0123456789"
	testAdminToken  = "test-admin-token-0123456789"
)

// newTestServer creates a Server backed by a temp-dir SQLite file and an
// httptest.Server wrapping its router. Both are cleaned up automatically.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("TOKEN", testClientToken)
	t.Setenv("ADMIN_TOKEN", testAdminToken)

	s, err := NewServer(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ts := httptest.NewServer(s.setupRoutes())
	t.Cleanup(ts.Close)
	return s, ts
}

// doRequest sends method+path with the given bearer token and JSON body,
// asserts the response status, and returns the response body bytes.
func doRequest(t *testing.T, ts *httptest.Server, method, path, token, body string, wantStatus int) []byte {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d (body: %s)", method, path, resp.StatusCode, wantStatus, data)
	}
	return data
}

// adminReq is doRequest with the admin token.
func adminReq(t *testing.T, ts *httptest.Server, method, path, body string, wantStatus int) []byte {
	t.Helper()
	return doRequest(t, ts, method, path, testAdminToken, body, wantStatus)
}

// clientSync GETs /client/sync with the client token and decodes the response.
func clientSync(t *testing.T, ts *httptest.Server, query string) ClientSyncResponse {
	t.Helper()
	data := doRequest(t, ts, "GET", "/client/sync"+query, testClientToken, "", http.StatusOK)
	var out ClientSyncResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	return out
}

func TestHealth(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./...`
Expected: compile FAIL — `too many arguments in call to NewServer`

- [ ] **Step 3: Parameterize NewServer**

In `server/main.go` change the constructor signature (line 29) and the call site (line 66):

```go
func NewServer(dbPath string) (*Server, error) {
	db, err := openDatabase(dbPath)
```

```go
	server, err := NewServer("./guardian.db")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS (`TestHealth`)

- [ ] **Step 5: Commit**

```bash
git add server/main.go server/server_test.go
git commit -m "test(server): add httptest harness; parameterize NewServer db path"
```

---

## Phase 1 — Critical auth fixes

### Task 2: Remove hardcoded fallback tokens; fail-fast startup validation (SEC-01, SEC-04)

**Files:**
- Modify: `server/middleware.go` (full rewrite below)
- Modify: `server/main.go:62-70`
- Modify: `agent/main.go:70-73`
- Create: `agent/main_test.go`
- Test: `server/middleware_test.go`

- [ ] **Step 1: Write the failing tests**

Create `server/middleware_test.go`:

```go
package main

import (
	"net/http"
	"testing"
)

func TestValidateTokens(t *testing.T) {
	long := "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name    string
		token   string
		admin   string
		wantErr bool
	}{
		{"valid", long + "c", long + "a", false},
		{"missing token", "", long, true},
		{"missing admin", long, "", true},
		{"token too short", "short", long, true},
		{"admin too short", long, "short", true},
		{"client placeholder", "your_token_here", long, true},
		{"admin placeholder", long, "your_admin_token_here", true},
		{"identical tokens", long, long, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TOKEN", tc.token)
			t.Setenv("ADMIN_TOKEN", tc.admin)
			err := ValidateTokens()
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTokens() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestAuthTiers(t *testing.T) {
	_, ts := newTestServer(t)
	cases := []struct {
		name  string
		path  string
		token string
		want  int
	}{
		{"no token on client endpoint", "/client/sync", "", http.StatusUnauthorized},
		{"wrong token on client endpoint", "/client/sync", "wrong", http.StatusUnauthorized},
		{"client token accepted", "/client/sync", testClientToken, http.StatusOK},
		{"client token rejected on admin endpoint", "/status", testClientToken, http.StatusUnauthorized},
		{"admin token accepted", "/status", testAdminToken, http.StatusOK},
		{"old hardcoded fallback rejected", "/status", "mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doRequest(t, ts, "GET", tc.path, tc.token, "", tc.want)
		})
	}
}

func TestAuthRejectsAllWhenTokenUnset(t *testing.T) {
	_, ts := newTestServer(t)
	t.Setenv("TOKEN", "")
	// Even an empty bearer must not match an empty configured token.
	doRequest(t, ts, "GET", "/client/sync", "", http.StatusUnauthorized)
}
```

Create `agent/main_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestFetchSyncRequiresToken(t *testing.T) {
	t.Setenv("TOKEN", "")
	_, err := fetchSync("http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("expected TOKEN configuration error, got: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...` (in `server/`)
Expected: compile FAIL — `undefined: ValidateTokens`; after that, `old hardcoded fallback rejected` would fail against current code.

Run: `go test ./...` (in `agent/`)
Expected: FAIL — `fetchSync` falls back to the hardcoded token and returns a connection error instead of a TOKEN error.

- [ ] **Step 3: Rewrite server/middleware.go**

Replace the entire file with:

```go
package main

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

const minTokenLength = 16

var placeholderTokens = map[string]bool{
	"your_token_here":       true,
	"your_admin_token_here": true,
}

// ValidateTokens ensures TOKEN and ADMIN_TOKEN are set, long enough, not
// placeholder values, and distinct. The server refuses to start otherwise.
func ValidateTokens() error {
	token := os.Getenv("TOKEN")
	admin := os.Getenv("ADMIN_TOKEN")
	for _, v := range []struct{ name, value string }{
		{"TOKEN", token},
		{"ADMIN_TOKEN", admin},
	} {
		if v.value == "" {
			return fmt.Errorf("%s is not set; set it in .env or the environment", v.name)
		}
		if len(v.value) < minTokenLength {
			return fmt.Errorf("%s must be at least %d characters", v.name, minTokenLength)
		}
		if placeholderTokens[v.value] {
			return fmt.Errorf("%s is still the placeholder value; generate a real token (e.g. openssl rand -hex 24)", v.name)
		}
	}
	if token == admin {
		return fmt.Errorf("TOKEN and ADMIN_TOKEN must be different values")
	}
	return nil
}

func authMiddleware(envVar string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := os.Getenv(envVar)
			expected := "Bearer " + token
			actual := r.Header.Get("Authorization")

			if token == "" || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
				slog.Warn("unauthorized access attempt", "remote", r.RemoteAddr, "path", r.URL.Path)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func AdminAuth(next http.Handler) http.Handler {
	return authMiddleware("ADMIN_TOKEN")(next)
}

func ClientAuth(next http.Handler) http.Handler {
	return authMiddleware("TOKEN")(next)
}
```

- [ ] **Step 4: Call ValidateTokens at startup**

In `server/main.go`, immediately after the `loadEnvFile` block (after line 64):

```go
	if err := ValidateTokens(); err != nil {
		slog.Error("invalid token configuration", "error", err)
		os.Exit(1)
	}
```

- [ ] **Step 5: Remove the agent fallback token**

In `agent/main.go` replace lines 70-73:

```go
	token := os.Getenv("TOKEN")
	if token == "" {
		return nil, fmt.Errorf("TOKEN is not set; configure it in the agent's .env file")
	}
```

(Also change `var token = ...` to the `:=` form shown; the rest of `fetchSync` is unchanged.)

- [ ] **Step 6: Run all tests**

Run: `go test ./...` in `server/`, then `go test ./...` in `agent/`
Expected: PASS in both

- [ ] **Step 7: Commit**

```bash
git add server/middleware.go server/middleware_test.go server/main.go agent/main.go agent/main_test.go
git commit -m "fix(security)!: remove hardcoded fallback tokens; validate tokens at startup (SEC-01, SEC-04)"
```

Note: this is a breaking change — existing installs without a real `.env` will refuse to start. That is intentional.

---

### Task 3: Default-deny unknown computers on first sync (SEC-03)

**Files:**
- Modify: `server/handlers.go:16-29`
- Test: `server/handlers_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `server/handlers_test.go`:

```go
package main

import (
	"testing"
)

// A computer never seen before must be treated as blocked (default-deny),
// matching the blocked=1 default used when its row is first inserted.
func TestSyncUnknownComputerIsBlocked(t *testing.T) {
	_, ts := newTestServer(t)
	adminReq(t, ts, "POST", "/manage/applications", `{"name":"game.exe","mode":"blacklist"}`, 201)
	adminReq(t, ts, "PUT", "/status", `{"enabled":true,"mode":"blacklist"}`, 200)

	resp := clientSync(t, ts, "?identity=999")
	if resp.Mode == "free" {
		t.Fatalf("first sync of unknown computer returned mode=free; want enforcement")
	}
	if len(resp.Applications) != 1 {
		t.Fatalf("got %d applications, want 1", len(resp.Applications))
	}
}

func TestSyncExplicitlyUnblockedComputerIsFree(t *testing.T) {
	_, ts := newTestServer(t)
	adminReq(t, ts, "POST", "/manage/applications", `{"name":"game.exe","mode":"blacklist"}`, 201)
	adminReq(t, ts, "PUT", "/status", `{"enabled":true,"mode":"blacklist"}`, 200)
	adminReq(t, ts, "PUT", "/manage/computers", `{"identity":7,"blocked":false}`, 200)

	resp := clientSync(t, ts, "?identity=7")
	if resp.Mode != "free" {
		t.Fatalf("mode = %q, want \"free\" for explicitly unblocked computer", resp.Mode)
	}
	if len(resp.Applications) != 0 {
		t.Fatalf("got %d applications, want 0", len(resp.Applications))
	}
}
```

- [ ] **Step 2: Run tests to verify the first one fails**

Run: `go test ./... -run TestSync`
Expected: `TestSyncUnknownComputerIsBlocked` FAIL (`mode=free`), `TestSyncExplicitlyUnblockedComputerIsFree` PASS

- [ ] **Step 3: Fix the ErrNoRows handling**

In `server/handlers.go` replace lines 16-29 with:

```go
	if identityStr != "" {
		if identity, err := parseIntParam(identityStr); err == nil {
			var blocked bool
			err := s.db.QueryRow("SELECT blocked FROM computers WHERE identity = ?", identity).Scan(&blocked)
			switch err {
			case nil:
				isComputerBlocked = blocked
			case sql.ErrNoRows:
				// Default-deny: unknown computers are blocked, matching the
				// blocked=1 default applied when the row is inserted below.
				isComputerBlocked = true
			}
			if err == nil || err == sql.ErrNoRows {
				if err := s.updateComputerDateTime(identity); err != nil {
					slog.Error("failed to update computer record", "identity", identity, "error", err)
				}
			}
		} else {
			slog.Warn("invalid identity parameter", "identity", identityStr)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/handlers.go server/handlers_test.go
git commit -m "fix(security): default-deny unknown computers on first sync (SEC-03)"
```

---

### Task 4: Request body size limit (SEC-06)

**Files:**
- Modify: `server/middleware.go` (append), `server/routes.go:11`
- Test: `server/middleware_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `server/middleware_test.go`:

```go
func TestBodySizeLimit(t *testing.T) {
	_, ts := newTestServer(t)
	huge := `{"name":"` + strings.Repeat("a", 2<<20) + `"}` // ~2 MiB
	doRequest(t, ts, "POST", "/manage/applications", testAdminToken, huge, http.StatusBadRequest)
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestBodySizeLimit`
Expected: FAIL — status 201 (oversized name accepted)

- [ ] **Step 3: Add the middleware**

Append to `server/middleware.go`:

```go
const maxBodyBytes = 1 << 20 // 1 MiB

// LimitBody caps request body size; json.Decode fails once the cap is hit
// and the handler returns 400.
func LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}
```

In `server/routes.go`, register it first, before the CORS middleware (line 11):

```go
	r.Use(LimitBody)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/middleware.go server/middleware_test.go server/routes.go
git commit -m "fix(security): limit request bodies to 1 MiB (SEC-06)"
```

---

### Task 5: Per-IP rate limiting (SEC-05)

**Files:**
- Modify: `server/go.mod` (new dep `github.com/go-chi/httprate`), `server/routes.go`
- Test: `server/middleware_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `server/middleware_test.go`:

```go
func TestRateLimit(t *testing.T) {
	_, ts := newTestServer(t)
	limited := false
	for i := 0; i < 301; i++ {
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected 429 after exceeding rate limit, never got one")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestRateLimit`
Expected: FAIL — `never got one`

- [ ] **Step 3: Add the dependency and middleware**

Run: `go get github.com/go-chi/httprate@latest`

In `server/routes.go` add to imports:

```go
	"time"

	"github.com/go-chi/httprate"
```

Register after `LimitBody` (300 req/min per IP supports ~50 agents per NAT at the 10s poll interval, with headroom for the mobile app):

```go
	r.Use(httprate.LimitByIP(300, time.Minute))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/go.mod server/go.sum server/routes.go server/middleware_test.go
git commit -m "feat(security): per-IP rate limiting, 300 req/min (SEC-05)"
```

---

### Task 6: Optional TLS (SEC-02)

No unit test — verified manually with a self-signed cert.

**Files:**
- Modify: `server/main.go:95-102`
- Modify: `dist/server/server.env`

- [ ] **Step 1: Read TLS env vars and branch the listener**

In `server/main.go` replace the serve goroutine (lines 95-102) with:

```go
	certFile := os.Getenv("TLS_CERT")
	keyFile := os.Getenv("TLS_KEY")

	go func() {
		var err error
		if certFile != "" && keyFile != "" {
			fmt.Printf("Guardian Server starting on %s (TLS)\n", addr)
			fmt.Printf("API: %s\n", displayAddr)
			err = httpServer.ListenAndServeTLS(certFile, keyFile)
		} else {
			fmt.Printf("Guardian Server starting on %s (PLAINTEXT — set TLS_CERT/TLS_KEY for HTTPS)\n", addr)
			fmt.Printf("API: %s\n", displayAddr)
			err = httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()
```

- [ ] **Step 2: Update the env template**

Replace `dist/server/server.env` with:

```
SERVER_ADDRESS=http://0.0.0.0:8080
TOKEN=your_token_here
ADMIN_TOKEN=your_admin_token_here
# Optional TLS. Generate a self-signed cert with:
#   openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
#     -keyout server.key -out server.crt -days 3650 -nodes -subj "/CN=guardian"
# TLS_CERT=/var/lib/guardian/server.crt
# TLS_KEY=/var/lib/guardian/server.key
```

- [ ] **Step 3: Verify manually**

```bash
cd server && go build -o /tmp/guardian-server .
cd /tmp && openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -keyout server.key -out server.crt -days 1 -nodes -subj "/CN=test"
TOKEN=0123456789abcdef0123456789abcdef ADMIN_TOKEN=fedcba9876543210fedcba9876543210 \
  TLS_CERT=/tmp/server.crt TLS_KEY=/tmp/server.key /tmp/guardian-server &
curl -sk https://localhost:8080/health
kill %1
```

Expected: `{"status":"ok"}` over HTTPS.

- [ ] **Step 4: Run tests + commit**

Run: `go test ./...` — Expected: PASS

```bash
git add server/main.go dist/server/server.env
git commit -m "feat(security): optional TLS via TLS_CERT/TLS_KEY (SEC-02)"
```

---

## Phase 2 — Server correctness

### Task 7: SQLite WAL + busy timeout (BUG-04)

**Files:**
- Modify: `server/db.go:9-15`
- Test: `server/db_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `server/db_test.go`:

```go
package main

import (
	"path/filepath"
	"testing"
)

func TestDatabasePragmas(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "pragma.db"))
	if err != nil {
		t.Fatalf("openDatabase: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want \"wal\"", mode)
	}

	var timeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if timeout < 5000 {
		t.Fatalf("busy_timeout = %d, want >= 5000", timeout)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestDatabasePragmas`
Expected: FAIL — `journal_mode = "delete"`

- [ ] **Step 3: Set pragmas in the DSN**

Replace `openDatabase` in `server/db.go`:

```go
func openDatabase(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}
	// modernc.org/sqlite applies _pragma per connection; a single connection
	// also serializes writes and avoids SQLITE_BUSY entirely.
	db.SetMaxOpenConns(1)
	return db, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/db.go server/db_test.go
git commit -m "fix(server): enable WAL and busy_timeout, single connection (BUG-04)"
```

---

### Task 8: `PUT /status` must not disable enforcement when `enabled` is omitted (BUG-02)

**Files:**
- Modify: `server/handlers.go:276-307` (`handleUpdateStatus`)
- Test: `server/handlers_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `server/handlers_test.go`:

```go
import (
	"encoding/json"
	...
)

func TestUpdateStatusModeOnlyKeepsEnabled(t *testing.T) {
	_, ts := newTestServer(t)
	adminReq(t, ts, "PUT", "/status", `{"enabled":true,"mode":"blacklist"}`, 200)
	// Changing only the mode must not flip enabled back to false.
	adminReq(t, ts, "PUT", "/status", `{"mode":"whitelist"}`, 200)

	data := adminReq(t, ts, "GET", "/status", "", 200)
	var st StatusResponse
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Enabled {
		t.Fatal("enabled flipped to false by a mode-only update")
	}
	if st.Mode != "whitelist" {
		t.Fatalf("mode = %q, want \"whitelist\"", st.Mode)
	}
}
```

(Add `"encoding/json"` to the file's import block if not present.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestUpdateStatusModeOnly`
Expected: FAIL — `enabled flipped to false`

- [ ] **Step 3: Decode into a pointer-field request struct**

In `handleUpdateStatus` replace the decode + assignment portion (the function currently decodes directly into `StatusResponse`):

```go
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool  `json:"enabled"`
		Mode    string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Invalid JSON"})
		return
	}

	if req.Mode != "" && req.Mode != "blacklist" && req.Mode != "whitelist" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "Mode must be 'blacklist' or 'whitelist'"})
		return
	}

	s.mu.Lock()
	if req.Enabled != nil {
		s.enabledCache = *req.Enabled
	}
	if req.Mode != "" {
		s.modeCache = req.Mode
	}
	enabled := s.enabledCache
	mode := s.modeCache
	if err := s.saveStatusToDatabase(enabled, mode); err != nil {
		s.mu.Unlock()
		slog.Error("failed to save status", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "Failed to save status"})
		return
	}
	s.mu.Unlock()

	statusText := "disabled"
	if enabled {
		statusText = "enabled"
	}
	slog.Info("server status updated", "status", statusText, "mode", mode)
	writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Server %s (mode: %s)", statusText, mode)})
}
```

(Note this also fixes the pre-existing unsynchronized read of `s.modeCache` in the final log/response lines.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/handlers.go server/handlers_test.go
git commit -m "fix(server): mode-only status update no longer disables enforcement (BUG-02)"
```

---

### Task 9: Preserve last-seen timestamp on computer block/unblock (BUG-03)

**Files:**
- Modify: `server/handlers.go:400` (`handleUpdateComputer`)
- Test: `server/handlers_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `server/handlers_test.go`:

```go
func TestUpdateComputerPreservesDateTime(t *testing.T) {
	s, ts := newTestServer(t)
	_, err := s.db.Exec("INSERT INTO computers (identity, blocked, datetime) VALUES (5, 0, '2020-01-01 00:00:00')")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	adminReq(t, ts, "PUT", "/manage/computers", `{"identity":5,"blocked":true}`, 200)

	var dt string
	if err := s.db.QueryRow("SELECT datetime FROM computers WHERE identity = 5").Scan(&dt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if dt != "2020-01-01 00:00:00" {
		t.Fatalf("datetime = %q; admin block action overwrote last-seen", dt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestUpdateComputerPreserves`
Expected: FAIL — datetime reset to current time

- [ ] **Step 3: Switch to an upsert**

In `handleUpdateComputer` replace the `INSERT OR REPLACE` exec:

```go
	_, err := s.db.Exec(`INSERT INTO computers (identity, blocked) VALUES (?, ?)
		ON CONFLICT(identity) DO UPDATE SET blocked = excluded.blocked`, req.Identity, req.Blocked)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/handlers.go server/handlers_test.go
git commit -m "fix(server): preserve computer last-seen on block/unblock (BUG-03)"
```

---

### Task 10: Server-side application name validation (IMP-02, supports SEC-08)

**Files:**
- Modify: `server/helpers.go` (append), `server/handlers.go:83-85` (`handleAddApplication`)
- Test: `server/helpers_test.go` (create)

- [ ] **Step 1: Write the failing tests**

Create `server/helpers_test.go`:

```go
package main

import "testing"

func TestValidateAppName(t *testing.T) {
	valid := []string{"chrome.exe", "game launcher.exe", "my-app_2", "abc"}
	for _, name := range valid {
		if err := validateAppName(name); err != nil {
			t.Errorf("validateAppName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{
		"",            // empty
		"ab",          // too short — would substring-match too much
		"a|b",         // shell/regex metacharacter
		"x*",          // regex metacharacter
		".hidden",     // must start alphanumeric
		"name\nx",     // control character
		string(make([]byte, 70)), // too long
	}
	for _, name := range invalid {
		if err := validateAppName(name); err == nil {
			t.Errorf("validateAppName(%q) = nil, want error", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestValidateAppName`
Expected: compile FAIL — `undefined: validateAppName`

- [ ] **Step 3: Implement and wire in**

Append to `server/helpers.go` (add `"regexp"` to imports):

```go
// appNameRe constrains names to a safe charset: process names are matched as
// substrings on agents and must never contain regex/shell metacharacters.
// Minimum 3 chars so a blacklist entry can't substring-match half the OS.
var appNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 ._-]{2,63}$`)

func validateAppName(name string) error {
	if !appNameRe.MatchString(name) {
		return fmt.Errorf("application name must be 3-64 characters, start alphanumeric, and contain only letters, digits, spaces, '.', '_', '-'")
	}
	return nil
}
```

In `handleAddApplication` replace the empty-name check (`server/handlers.go:83-86`) with:

```go
	req.Name = strings.TrimSpace(req.Name)
	if err := validateAppName(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS. If `TestSync*` tests fail because they use a short name, they already use `game.exe` (valid) — no change needed.

- [ ] **Step 5: Commit**

```bash
git add server/helpers.go server/helpers_test.go server/handlers.go
git commit -m "feat(server): validate application names (IMP-02)"
```

---

### Task 11: Remove dead and redundant code (BUG-06)

**Files:**
- Modify: `server/db.go:185-188`, `server/handlers.go:170-177`

- [ ] **Step 1: Delete `removeApplicationFromDatabase`**

Remove from `server/db.go`:

```go
func (s *Server) removeApplicationFromDatabase(name string) error {
	_, err := s.db.Exec("DELETE FROM applications WHERE name = ?", name)
	return err
}
```

- [ ] **Step 2: Drop the duplicate cache existence check**

In `handleUpdateApplication` (`server/handlers.go:170-177`), the second check repeats the first. Replace:

```go
	if cacheKey == "" {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Application '%s' not found", req.Name)})
		return
	}
	if _, exists := s.appsCache[cacheKey]; !exists {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Application '%s' not found", req.Name)})
		return
	}

	app := s.appsCache[cacheKey]
```

with:

```go
	app, exists := s.appsCache[cacheKey]
	if !exists {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: fmt.Sprintf("Application '%s' not found", req.Name)})
		return
	}
```

(When `cacheKey == ""` the map lookup misses, so the single check covers both cases.)

- [ ] **Step 3: Verify and commit**

Run: `go vet ./... && go test ./...`
Expected: PASS, no vet findings

```bash
git add server/db.go server/handlers.go
git commit -m "refactor(server): remove dead code and redundant cache check (BUG-06)"
```

---

## Phase 3 — Agent safety

### Task 12: Extract testable process-matching functions (SEC-08 groundwork)

The kill decisions currently live inline in two near-identical loops (`main.go` console, `service_windows.go` service) and match against raw `tasklist` text. Extract pure functions into a new file so they can be tested on any OS.

**Files:**
- Create: `agent/match.go`
- Create: `agent/match_test.go`
- Modify: `agent/main.go:323-345` (move `extractProcessName` into `match.go`, refactored)

- [ ] **Step 1: Write the failing tests**

Create `agent/match_test.go`:

```go
package main

import (
	"reflect"
	"testing"
)

func TestExtractProcessNameFor(t *testing.T) {
	cases := []struct {
		goos, line, want string
	}{
		{"windows", `"chrome.exe","1234","Console","1","100,000 K"`, "chrome.exe"},
		{"windows", `not csv`, ""},
		{"windows", "", ""},
		{"linux", "user 123 0.0 0.1 1000 2000 ? S 10:00 0:01 /usr/bin/firefox -flag", "firefox"},
		{"darwin", "user 123 0.0 0.1 1000 2000 ? S 10:00 0:01 /Applications/Game.app/Contents/MacOS/game", "game"},
		{"linux", "short line", ""},
	}
	for _, tc := range cases {
		if got := extractProcessNameFor(tc.goos, tc.line); got != tc.want {
			t.Errorf("extractProcessNameFor(%q, %q) = %q, want %q", tc.goos, tc.line, got, tc.want)
		}
	}
}

func TestMatchBlacklist(t *testing.T) {
	procs := []string{"chrome.exe", "svchost.exe", "lsass.exe", "Minecraft.exe", "procsentinel-agent64.exe"}
	apps := []ClientApplication{{Name: "chrome"}, {Name: "minecraft"}, {Name: "s"}}

	got := matchBlacklist(procs, apps)
	want := []string{"chrome.exe", "Minecraft.exe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchBlacklist = %v, want %v", got, want)
	}
	// The single-letter entry "s" must NOT have matched svchost.exe or
	// lsass.exe: system processes are protected in blacklist mode too.
}

func TestMatchBlacklistEmptyNameIgnored(t *testing.T) {
	got := matchBlacklist([]string{"chrome.exe"}, []ClientApplication{{Name: ""}})
	if len(got) != 0 {
		t.Fatalf("empty app name matched: %v", got)
	}
}

func TestMatchWhitelist(t *testing.T) {
	procs := []string{"chrome.exe", "svchost.exe", "notepad.exe", "notepad.exe"}
	apps := []ClientApplication{{Name: "chrome.exe"}}

	got := matchWhitelist(procs, apps)
	want := []string{"notepad.exe"} // svchost protected, chrome allowed, notepad deduplicated
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matchWhitelist = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...` (in `agent/`)
Expected: compile FAIL — `undefined: extractProcessNameFor`, `matchBlacklist`, `matchWhitelist`

- [ ] **Step 3: Create agent/match.go**

```go
package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

// extractProcessName extracts a process name from one line of the current
// platform's process listing.
func extractProcessName(line string) string {
	return extractProcessNameFor(runtime.GOOS, line)
}

func extractProcessNameFor(goos, line string) string {
	switch goos {
	case "windows":
		// tasklist /FO CSV /NH: "chrome.exe","1234","Console","1","100,000 K"
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '"' {
			return ""
		}
		end := strings.Index(line[1:], "\"")
		if end < 0 {
			return ""
		}
		return line[1 : end+1]
	case "linux", "darwin":
		// ps aux: user PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND...
		fields := strings.Fields(line)
		if len(fields) >= 11 {
			return filepath.Base(fields[10])
		}
	}
	return ""
}

// processNames parses raw process-list output into deduplicated process names.
func processNames(processList string) []string {
	var names []string
	for _, line := range strings.Split(processList, "\n") {
		if name := extractProcessName(strings.TrimSpace(line)); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// matchBlacklist returns the running process names that match a blacklisted
// application. Matching is case-insensitive substring against the process
// name only (never raw listing text), and system processes are never matched.
func matchBlacklist(procNames []string, apps []ClientApplication) []string {
	var victims []string
	seen := make(map[string]bool)
	for _, procName := range procNames {
		lower := strings.ToLower(procName)
		if seen[lower] || isSystemProcess(procName) {
			continue
		}
		for _, app := range apps {
			if app.Name == "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(app.Name)) {
				victims = append(victims, procName)
				seen[lower] = true
				break
			}
		}
	}
	return victims
}

// matchWhitelist returns the running process names that are neither
// whitelisted nor protected system processes.
func matchWhitelist(procNames []string, apps []ClientApplication) []string {
	allowed := make(map[string]bool, len(apps))
	for _, app := range apps {
		allowed[strings.ToLower(app.Name)] = true
	}
	var victims []string
	seen := make(map[string]bool)
	for _, procName := range procNames {
		lower := strings.ToLower(procName)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		if !allowed[lower] && !isSystemProcess(procName) {
			victims = append(victims, procName)
		}
	}
	return victims
}
```

Delete the old `extractProcessName` from `agent/main.go:323-345` (now in `match.go`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...` and `GOOS=windows go build ./...`
Expected: PASS, clean cross-build

- [ ] **Step 5: Commit**

```bash
git add agent/match.go agent/match_test.go agent/main.go
git commit -m "refactor(agent): extract testable process matching with system-process protection"
```

---

### Task 13: Use safe matching in both loops; kill by exact name (SEC-08, BUG-05)

**Files:**
- Modify: `agent/main.go:255-321` (console loop), `agent/main.go:523-533` (`killProcess`)
- Modify: `agent/service_windows.go:158-221` (service loop)

- [ ] **Step 1: Rewrite the console monitoring loop body**

In `agent/main.go` replace the loop body from the `state.Mode == "free"` check through the end of the whitelist branch (lines 250-317) with (note the power check now comes **before** the empty-list short-circuit — BUG-05):

```go
		if state.Mode == "free" {
			time.Sleep(time.Second)
			continue
		}

		// Check power status before the empty-list short-circuit so remote
		// shutdown works even with an empty blacklist (BUG-05).
		if powerStatus, found := getClientEntry(state, "power"); found && !powerStatus {
			log.Println("Shutdown PC triggered: power disabled")
			if err := shutdownPCService(); err != nil {
				log.Printf("Failed to shutdown PC: %v", err)
			}
			time.Sleep(60 * time.Second)
			continue
		}

		if len(state.Applications) == 0 && state.Mode != "whitelist" {
			time.Sleep(time.Second)
			continue
		}

		// Get list of running processes
		processes, err := getProcessList()
		if err != nil {
			log.Printf("Error getting process list: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		names := processNames(processes)

		// Copy current state to avoid race
		localApps := make([]ClientApplication, len(state.Applications))
		copy(localApps, state.Applications)
		localMode := state.Mode

		var victims []string
		if localMode == "blacklist" {
			victims = matchBlacklist(names, localApps)
		} else if localMode == "whitelist" {
			victims = matchWhitelist(names, localApps)
		}
		for _, victim := range victims {
			if err := killProcess(victim); err == nil {
				log.Printf("Killed process: %s", victim)
			} else {
				log.Printf("Failed to kill process %s: %v", victim, err)
			}
		}

		time.Sleep(time.Second)
```

- [ ] **Step 2: Make killProcess exact-match**

Replace `killProcess` in `agent/main.go` (add `"regexp"` to imports):

```go
// killProcess terminates a process by its exact name. Callers pass names
// extracted from the live process list, never raw admin input.
func killProcess(name string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("taskkill", "/F", "/IM", name).Run()
	case "linux", "darwin":
		// -x matches the whole process name; QuoteMeta prevents the name
		// being interpreted as a regex (pkill patterns are ERE).
		return exec.Command("pkill", "-x", regexp.QuoteMeta(name)).Run()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}
```

- [ ] **Step 3: Apply the same loop rewrite to the service loop**

In `agent/service_windows.go` replace the `case <-ticker.C:` body (lines 158-219) with:

```go
		case <-ticker.C:
			if state == nil {
				continue
			}

			if state.Mode == "free" {
				continue
			}

			// Power check before the empty-list short-circuit (BUG-05).
			if powerStatus, found := getClientEntry(state, "power"); found && !powerStatus {
				elog.Info(1, "Shutdown PC triggered: power disabled")
				if err := shutdownPCService(); err != nil {
					elog.Error(1, fmt.Sprintf("Failed to shutdown PC: %v", err))
				}
				continue
			}

			if len(state.Applications) == 0 && state.Mode != "whitelist" {
				continue
			}

			processes, err := getProcessList()
			if err != nil {
				elog.Error(1, fmt.Sprintf("Error getting process list: %v", err))
				continue
			}

			names := processNames(processes)

			localApps := make([]ClientApplication, len(state.Applications))
			copy(localApps, state.Applications)
			localMode := state.Mode

			var victims []string
			if localMode == "blacklist" {
				victims = matchBlacklist(names, localApps)
			} else if localMode == "whitelist" {
				victims = matchWhitelist(names, localApps)
			}
			for _, victim := range victims {
				if err := killProcess(victim); err == nil {
					elog.Info(1, fmt.Sprintf("Killed process: %s", victim))
				} else {
					elog.Error(1, fmt.Sprintf("Failed to kill process %s: %v", victim, err))
				}
			}
```

Note: the service loop previously lacked the `state.Mode == "free"` check that the console loop had — this rewrite adds it (the server returns `mode: "free"` precisely to mean "do not enforce").

- [ ] **Step 4: Verify**

Run: `go test ./...` and `GOOS=windows go build ./...`
Expected: PASS, clean cross-build. The matching behavior itself is covered by Task 12's tests; the loops are now thin glue.

- [ ] **Step 5: Commit**

```bash
git add agent/main.go agent/service_windows.go
git commit -m "fix(agent): protect system processes in blacklist mode, exact-name kills, power check before empty-list skip (SEC-08, BUG-05)"
```

---

### Task 14: Fix the sync-state data race (BUG-01)

**Files:**
- Modify: `agent/main.go:109-129,219-247` (console), `agent/service_windows.go:124-146,152-160,225-258` (service)
- Test: existing tests under `-race`

- [ ] **Step 1: Switch console mode to atomic.Pointer**

In `agent/main.go` add `"sync/atomic"` to imports. Change `updateSync`:

```go
// updateSync fetches updated sync state from server
func updateSync(serverAddress string, state *atomic.Pointer[SyncResponse]) {
	for {
		resp, err := fetchSync(serverAddress)
		if err != nil {
			log.Printf("Failed to sync: %v. Loading from sync.json.", err)
			if cached, loadErr := loadSyncFromFile(); loadErr == nil {
				state.Store(cached)
				log.Printf("Loaded %d applications from sync.json", len(cached.Applications))
			} else {
				log.Printf("Could not load sync.json: %v", loadErr)
			}
		} else {
			state.Store(resp)
			if err := saveSyncToFile(resp); err != nil {
				log.Printf("Failed to save sync.json: %v", err)
			}
			log.Printf("Synced: %d applications, mode=%s", len(resp.Applications), resp.Mode)
		}
		time.Sleep(10 * time.Second)
	}
}
```

In `runConsole` replace `var state *SyncResponse` with `var state atomic.Pointer[SyncResponse]`, pass `&state` to `go updateSync(serverAddress, &state)`, replace the initial-fetch assignments `state = cached` / `state = resp` / `state = &SyncResponse{}` with `state.Store(...)`, and start each loop iteration with:

```go
	for {
		st := state.Load()
		if st == nil {
			time.Sleep(time.Second)
			continue
		}
```

then use `st` instead of `state` throughout the loop body (`st.Mode`, `st.Applications`, `getClientEntry(st, "power")`). The explicit `localApps` copy can stay — `st` is already an immutable snapshot, but the copy is harmless.

- [ ] **Step 2: Apply the same change to service mode**

In `agent/service_windows.go`: `updateSyncService(serverAddress string, state *atomic.Pointer[SyncResponse], stopCh <-chan bool)` with `state.Store(...)` in both branches; in `runAgent` declare `var state atomic.Pointer[SyncResponse]`, store the initial fetch results, and load a snapshot at the top of each tick:

```go
		case <-ticker.C:
			st := state.Load()
			if st == nil {
				continue
			}
```

(using `st` for all subsequent reads). Add `"sync/atomic"` to imports.

- [ ] **Step 3: Verify with the race detector**

Run: `go test -race ./...` and `GOOS=windows go build ./...`
Expected: PASS, no race reports, clean cross-build

- [ ] **Step 4: Commit**

```bash
git add agent/main.go agent/service_windows.go
git commit -m "fix(agent): eliminate data race on sync state with atomic.Pointer (BUG-01)"
```

---

### Task 15: Agent state files written 0600 (SEC-13)

**Files:**
- Modify: `agent/main.go:374,494` (`generateWhitelist`, `saveSyncToFile`)

- [ ] **Step 1: Tighten file modes**

Change both `os.WriteFile(..., 0644)` calls to `0600`:

```go
	return os.WriteFile(whitelistFile, []byte(content), 0600)
```

```go
	return os.WriteFile("sync.json", data, 0600)
```

- [ ] **Step 2: Verify and commit**

Run: `go test ./...`
Expected: PASS

```bash
git add agent/main.go
git commit -m "fix(agent): write whitelist.txt and sync.json 0600 (SEC-13)"
```

---

## Phase 4 — Deployment hardening

### Task 16: Non-root systemd unit + token-generating installer (SEC-07, SEC-04)

State moves from `/usr/local/bin/guardian/` to `/var/lib/guardian/`. **Migration note for existing installs:** the installer copies an existing `guardian.db` and `.env` from the old location if present.

**Files:**
- Modify: `dist/server/guardian-server.service` (full rewrite)
- Modify: `dist/server/install.sh` (full rewrite)

- [ ] **Step 1: Rewrite the systemd unit**

Replace `dist/server/guardian-server.service`:

```ini
[Unit]
Description=Guardian Server
After=network.target

[Service]
User=guardian
Group=guardian
WorkingDirectory=/var/lib/guardian
ExecStart=/usr/local/bin/guardian-server
Restart=always
RestartSec=5

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
ReadWritePaths=/var/lib/guardian

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: Rewrite install.sh**

Replace `dist/server/install.sh`:

```bash
#!/bin/bash
# Guardian server installer. Requires root.
set -euo pipefail

if [ "$EUID" -ne 0 ]; then
  echo "Switching to root..."
  exec sudo bash "$0" "$@"
fi

OLD_DIR=/usr/local/bin/guardian
STATE_DIR=/var/lib/guardian

id -u guardian &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin guardian

install -m 755 guardian-server /usr/local/bin/guardian-server
mkdir -p "$STATE_DIR"

# Migrate state from the legacy install location
if [ -d "$OLD_DIR" ]; then
  [ -f "$OLD_DIR/guardian.db" ] && [ ! -f "$STATE_DIR/guardian.db" ] && cp "$OLD_DIR/guardian.db" "$STATE_DIR/"
  [ -f "$OLD_DIR/.env" ] && [ ! -f "$STATE_DIR/.env" ] && cp "$OLD_DIR/.env" "$STATE_DIR/"
fi

# First install: generate real tokens instead of shipping placeholders
if [ ! -f "$STATE_DIR/.env" ]; then
  TOKEN=$(openssl rand -hex 24)
  ADMIN_TOKEN=$(openssl rand -hex 24)
  sed -e "s|^TOKEN=.*|TOKEN=${TOKEN}|" \
      -e "s|^ADMIN_TOKEN=.*|ADMIN_TOKEN=${ADMIN_TOKEN}|" \
      server.env > "$STATE_DIR/.env"
  echo "Generated tokens (saved in $STATE_DIR/.env):"
  echo "  TOKEN=${TOKEN}          <- agents"
  echo "  ADMIN_TOKEN=${ADMIN_TOKEN}    <- mobile app"
fi

chown -R guardian:guardian "$STATE_DIR"
chmod 600 "$STATE_DIR/.env"

cp guardian-server.service /etc/systemd/system/guardian-server.service
systemctl daemon-reload
systemctl enable guardian-server
systemctl restart guardian-server
systemctl status --no-pager guardian-server
```

- [ ] **Step 3: Verify**

Run: `bash -n dist/server/install.sh`
Expected: no syntax errors. (Full verification requires a Linux host; note this in the PR.)

- [ ] **Step 4: Commit**

```bash
git add dist/server/guardian-server.service dist/server/install.sh
git commit -m "feat(deploy): non-root hardened systemd unit; installer generates tokens (SEC-07, SEC-04)"
```

---

### Task 17: Non-root Docker image (SEC-07)

**Files:**
- Modify: `dist/server/Dockerfile`

- [ ] **Step 1: Rewrite the Dockerfile**

```dockerfile
FROM alpine:3.21
RUN addgroup -S guardian && adduser -S -G guardian guardian \
    && mkdir -p /data && chown guardian:guardian /data
COPY guardian-server /usr/local/bin/
USER guardian
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget -qO- http://127.0.0.1:8080/health || exit 1
CMD ["guardian-server"]
```

- [ ] **Step 2: Verify (if Docker is available)**

```bash
cd server && GOOS=linux GOARCH=amd64 go build -o ../dist/server/guardian-server . && cd ../dist/server
docker build -t guardian-test . && docker run --rm guardian-test id
rm guardian-server
```

Expected: build succeeds; `id` shows non-root uid. (Note: container will exit on missing tokens — that's Task 2 working as intended; `id` check is enough here.)

- [ ] **Step 3: Commit**

```bash
git add dist/server/Dockerfile
git commit -m "feat(deploy): run Docker image as non-root with healthcheck (SEC-07)"
```

---

## Phase 5 — Mobile

### Task 18: Store the admin token in secure storage (SEC-10)

`flutter_secure_storage` backs onto the Android Keystore. One-time migration moves any token out of SharedPreferences. Plugin requires platform channels, so verification is `flutter analyze` + manual run, not unit tests.

**Files:**
- Modify: `mobile/pubspec.yaml` (dependencies)
- Modify: `mobile/lib/services/settings_service.dart:34-49`

- [ ] **Step 1: Add the dependency**

Run: `cd mobile && flutter pub add flutter_secure_storage`
Expected: `flutter_secure_storage` appears in `pubspec.yaml` dependencies.

- [ ] **Step 2: Replace token storage**

In `mobile/lib/services/settings_service.dart` add the import and a storage field, then replace `getToken`/`setToken`:

```dart
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
```

Inside the class (near the existing keys):

```dart
  static const FlutterSecureStorage _secureStorage = FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
  );
```

```dart
  /// Gets the authentication token from secure storage, migrating any
  /// token previously stored in plaintext SharedPreferences.
  Future<String?> getToken() async {
    var token = await _secureStorage.read(key: _tokenKey);
    if (token == null) {
      final prefs = await SharedPreferences.getInstance();
      token = prefs.getString(_tokenKey);
      if (token != null) {
        await _secureStorage.write(key: _tokenKey, value: token);
        await prefs.remove(_tokenKey);
      }
    }
    return token;
  }

  /// Sets the authentication token in secure storage
  Future<void> setToken(String? token) async {
    if (token == null || token.isEmpty) {
      await _secureStorage.delete(key: _tokenKey);
    } else {
      await _secureStorage.write(key: _tokenKey, value: token);
    }
    notifyListeners();
  }
```

- [ ] **Step 3: Verify**

Run: `flutter analyze && flutter build apk --debug`
Expected: no analyzer issues, successful build. Manual check: install on device, confirm a previously saved token still appears in Settings after upgrade.

- [ ] **Step 4: Commit**

```bash
git add mobile/pubspec.yaml mobile/pubspec.lock mobile/lib/services/settings_service.dart
git commit -m "fix(mobile): store admin token in secure storage with migration (SEC-10)"
```

---

### Task 19: "Test connection" must validate the token (IMP-07)

**Files:**
- Modify: `mobile/lib/services/settings_service.dart:69-97` (`testConnection`)

- [ ] **Step 1: Probe an authenticated endpoint**

Replace `testConnection`:

```dart
  /// Tests connection AND token validity against the authenticated /status
  /// endpoint (the unauthenticated /health gives false confidence: a wrong
  /// admin token would still report success).
  Future<bool> testConnection(String serverAddress) async {
    final client = http.Client();
    try {
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }
```

- [ ] **Step 2: Verify**

Run: `flutter analyze`
Expected: clean. Manual check: wrong token in Settings → "Test connection" now fails; correct token → succeeds.

- [ ] **Step 3: Commit**

```bash
git add mobile/lib/services/settings_service.dart
git commit -m "fix(mobile): test connection validates the admin token (IMP-07)"
```

---

## Phase 6 — Documentation sync

### Task 20: Update specs and CLAUDE.md (repo rule)

**Files:**
- Modify: `specs/server.md`, `specs/api.md`, `specs/agent.md`, `CLAUDE.md`

- [ ] **Step 1: Update the docs to reflect all changes in this plan**

Cover, at minimum:
- `specs/server.md`: mandatory token validation at startup (min 16 chars, distinct, no placeholders); new env vars `TLS_CERT`/`TLS_KEY`; rate limit (300 req/min/IP) and 1 MiB body cap; SQLite WAL + busy_timeout, single connection; `NewServer(dbPath)`; state directory `/var/lib/guardian`; non-root systemd unit; app-name validation rules; `PUT /status` partial-update semantics (`enabled` optional); computers upsert preserving `datetime`; unknown computers default-blocked on sync.
- `specs/api.md`: 401 on missing/placeholder tokens (no fallback); 429 responses; 400 for oversized bodies and invalid app names; `PUT /status` with optional `enabled`; `/client/sync` default-deny semantics for unknown identities.
- `specs/agent.md`: TOKEN now mandatory (no fallback); matching is per-process-name with system-process protection in **both** modes; kills are exact-name (`taskkill /IM`, `pkill -x` quoted); power check precedes the empty-list skip; `mode=free` honored in service mode; state files 0600; `atomic.Pointer` sync state.
- `CLAUDE.md`: add `go test ./...` (server, agent) to Build Commands; note `httprate` dependency; correct the dist description (`/var/lib/guardian` state dir, generated tokens, no `procsentinel-server.tar.gz`); add the new test files to the architecture listing.

- [ ] **Step 2: Commit**

```bash
git add specs/server.md specs/api.md specs/agent.md CLAUDE.md
git commit -m "docs: sync specs and CLAUDE.md with security hardening changes"
```

---

## Phase 7 — Mobile UI/UX

Pure functions get unit tests (`flutter test`); widget changes are verified with `flutter analyze` plus a manual checklist (no widget-test infrastructure exists yet, and adding it is out of scope). The Flutter package name is `procsentinel_mobile` — test imports must use `package:procsentinel_mobile/...`.

### Task 21: Fix timezone-broken online detection (BUG-07, UX-08)

SQLite `CURRENT_TIMESTAMP` values arrive as `"2026-06-12 08:15:00"` — UTC with no timezone marker, which `DateTime.parse` interprets as **local** time. The server's `current_time` carries an offset and parses correctly, so the comparison is skewed by the device's UTC offset and every computer shows "offline" outside UTC. Also raises the online threshold from 60s to 90s (the shipped `dist/agent/agent.env` uses `CHECK_INTERVAL=30`; 60s flickers on one missed poll) and switches "Last seen" to a 24-hour clock.

**Files:**
- Create: `mobile/lib/utils/time_utils.dart`
- Create: `mobile/test/time_utils_test.dart`
- Modify: `mobile/lib/screens/computers_screen.dart:255-286` (`_isComputerOnline`, `_formatLastSeen`)
- Modify: `mobile/lib/services/settings_service.dart:370-372` (`getComputersData` current_time parsing)

- [ ] **Step 1: Write the failing tests**

Create `mobile/test/time_utils_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:procsentinel_mobile/utils/time_utils.dart';

void main() {
  test('naive SQLite timestamp is parsed as UTC, not local', () {
    final t = parseServerTimestamp('2026-06-12 08:15:00');
    expect(t.isUtc, isTrue);
    expect(t, DateTime.utc(2026, 6, 12, 8, 15));
  });

  test('timestamp with explicit offset keeps its zone', () {
    final t = parseServerTimestamp('2026-06-12T08:15:00+03:00');
    expect(t.toUtc(), DateTime.utc(2026, 6, 12, 5, 15));
  });

  test('Z-suffixed timestamp is unchanged', () {
    final t = parseServerTimestamp('2026-06-12T08:15:00Z');
    expect(t, DateTime.utc(2026, 6, 12, 8, 15));
  });

  test('online within 90 seconds of server time', () {
    final server = DateTime.utc(2026, 6, 12, 8, 15);
    expect(isOnline(server, server.subtract(const Duration(seconds: 89))), isTrue);
    expect(isOnline(server, server.subtract(const Duration(seconds: 91))), isFalse);
  });
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd mobile && flutter test`
Expected: FAIL — `Error: Couldn't resolve the package 'procsentinel_mobile'... time_utils.dart` (file doesn't exist).

- [ ] **Step 3: Implement**

Create `mobile/lib/utils/time_utils.dart`:

```dart
/// Agents report every CHECK_INTERVAL seconds (20-30s in the shipped
/// configs); 90s tolerates one missed poll before showing "offline".
const Duration onlineThreshold = Duration(seconds: 90);

/// Parses a server timestamp. SQLite CURRENT_TIMESTAMP values arrive as
/// "2026-06-12 08:15:00" — UTC but with no timezone marker, which Dart
/// would otherwise interpret as local time.
DateTime parseServerTimestamp(String value) {
  final v = value.trim();
  final hasZone = v.endsWith('Z') || RegExp(r'[+-]\d{2}:?\d{2}$').hasMatch(v);
  return DateTime.parse(hasZone ? v : '${v}Z');
}

bool isOnline(DateTime serverTime, DateTime lastSeen) =>
    serverTime.difference(lastSeen).abs() < onlineThreshold;
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd mobile && flutter test`
Expected: all 4 tests PASS.

- [ ] **Step 5: Use the helpers in the UI and the service**

In `mobile/lib/screens/computers_screen.dart` add the import:

```dart
import '../utils/time_utils.dart';
```

Replace `_isComputerOnline` and `_formatLastSeen` (lines 255-286):

```dart
  bool _isComputerOnline(String datetimeStr) {
    if (_currentServerTime == null) return false;
    try {
      return isOnline(_currentServerTime!, parseServerTimestamp(datetimeStr));
    } catch (e) {
      return false;
    }
  }

  String _formatLastSeen(String datetimeStr) {
    try {
      final localTime = parseServerTimestamp(datetimeStr).toLocal();
      final hour = localTime.hour.toString().padLeft(2, '0');
      final minute = localTime.minute.toString().padLeft(2, '0');
      return '${_monthAbbr(localTime.month)} ${localTime.day}, $hour:$minute';
    } catch (e) {
      return datetimeStr;
    }
  }
```

In `mobile/lib/services/settings_service.dart` (lives in `lib/services/`, so the relative path goes up one level) add the import and replace the `current_time` parsing in `getComputersData` (line 370-372):

```dart
import '../utils/time_utils.dart';
```

```dart
          'current_time': body['current_time'] != null
              ? parseServerTimestamp(body['current_time'] as String)
              : null,
```

- [ ] **Step 6: Verify**

Run: `cd mobile && flutter analyze && flutter test`
Expected: no analyzer issues, tests pass. Manual check on a device in a non-UTC timezone: a machine that just synced shows the green "Online" dot.

- [ ] **Step 7: Commit**

```bash
git add mobile/lib/utils/time_utils.dart mobile/test/time_utils_test.dart mobile/lib/screens/computers_screen.dart mobile/lib/services/settings_service.dart
git commit -m "fix(mobile): parse naive server timestamps as UTC; 90s online threshold (BUG-07, UX-08)"
```

---

### Task 22: Distinguish "wrong token" from "server offline" (UX-03)

Builds on Task 19 (which pointed `testConnection` at `/status`). A 401 currently renders as "Server Not Connected" / empty lists, so a mistyped token looks like a network problem. Also deduplicates the `_buildDisconnected` widget that is copy-pasted across three screens.

**Files:**
- Modify: `mobile/lib/services/settings_service.dart` (`testConnection` → `checkConnection` + wrapper, top-level enum)
- Create: `mobile/lib/widgets/disconnected_view.dart`
- Modify: `mobile/lib/screens/home_screen.dart` (field at :20, `_loadData` :54 and :75, delete `_buildDisconnected` :339-365, build :504-505)
- Modify: `mobile/lib/screens/system_screen.dart` (field at :18, `_loadData` :50 and :67, delete `_buildDisconnected` :145-171, build :295-296)
- Modify: `mobile/lib/screens/computers_screen.dart` (field at :20, `_loadData` :58 and :78, delete `_buildDisconnected` :296-322, build :448-449)
- Modify: `mobile/lib/screens/settings_screen.dart:117-127` (`_testConnection` result message)

- [ ] **Step 1: Add the enum and `checkConnection` to the service**

In `mobile/lib/services/settings_service.dart`, add at top level (after the imports, before the class):

```dart
enum ConnectionStatus { connected, unauthorized, unreachable }
```

Replace `testConnection` (the Task 19 version) with:

```dart
  /// Distinguishes "server down" from "wrong token" so the UI can say
  /// which one the user has to fix.
  Future<ConnectionStatus> checkConnection(String serverAddress) async {
    final client = http.Client();
    try {
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .get(uri, headers: headers)
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 200) return ConnectionStatus.connected;
      if (response.statusCode == 401) return ConnectionStatus.unauthorized;
      return ConnectionStatus.unreachable;
    } catch (e) {
      return ConnectionStatus.unreachable;
    } finally {
      client.close();
    }
  }

  /// Boolean wrapper kept for callers that only need reachability
  /// (e.g. the nav-bar status icon in main.dart).
  Future<bool> testConnection(String serverAddress) async =>
      await checkConnection(serverAddress) == ConnectionStatus.connected;
```

- [ ] **Step 2: Create the shared disconnected view**

Create `mobile/lib/widgets/disconnected_view.dart`:

```dart
import 'package:flutter/material.dart';

import '../services/settings_service.dart';

/// Shared full-screen state for unreachable / unauthorized servers,
/// replacing the three copy-pasted `_buildDisconnected` methods.
class DisconnectedView extends StatelessWidget {
  const DisconnectedView({
    super.key,
    required this.status,
    required this.onRetry,
  });

  final ConnectionStatus status;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final unauthorized = status == ConnectionStatus.unauthorized;
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            unauthorized ? Icons.key_off : Icons.cloud_off,
            size: 72,
            color: Colors.grey.shade400,
          ),
          const SizedBox(height: 16),
          Text(
            unauthorized ? 'Invalid Token' : 'Server Not Connected',
            style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
          ),
          const SizedBox(height: 8),
          Text(
            unauthorized
                ? 'The server rejected the token.\nCheck it in Settings.'
                : 'Check your server settings and connection',
            textAlign: TextAlign.center,
            style: const TextStyle(color: Colors.grey),
          ),
          const SizedBox(height: 24),
          OutlinedButton.icon(
            onPressed: onRetry,
            icon: const Icon(Icons.refresh),
            label: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}
```

- [ ] **Step 3: Apply the same three-part change in each data screen**

In **each** of `home_screen.dart`, `system_screen.dart`, `computers_screen.dart`:

(a) Add the import:

```dart
import '../widgets/disconnected_view.dart';
```

(b) Replace the field `bool _isConnected = false;` with:

```dart
  ConnectionStatus _connectionStatus = ConnectionStatus.unreachable;

  bool get _isConnected => _connectionStatus == ConnectionStatus.connected;
```

(All other `_isConnected` reads keep working via the getter.)

(c) In `_loadData`, replace

```dart
      _isConnected = await _settingsService.testConnection(serverAddress);
```

with

```dart
      _connectionStatus = await _settingsService.checkConnection(serverAddress);
```

and in the `catch` block replace `_isConnected = false;` with `_connectionStatus = ConnectionStatus.unreachable;`.

(d) Delete the entire `_buildDisconnected()` method and replace its call site in `build` (`? _buildDisconnected()`) with:

```dart
              ? DisconnectedView(status: _connectionStatus, onRetry: _loadData)
```

- [ ] **Step 4: Make the Settings "Test" button report the distinction**

In `mobile/lib/screens/settings_screen.dart`, replace the body of the `try` block in `_testConnection` (lines 117-128):

```dart
      final status = await _settingsService.checkConnection(serverAddress);

      if (mounted) {
        final (message, color) = switch (status) {
          ConnectionStatus.connected => (
              'Connection successful!',
              Colors.green
            ),
          ConnectionStatus.unauthorized => (
              'Server reachable, but the token was rejected',
              Colors.orange
            ),
          ConnectionStatus.unreachable => (
              'Connection failed. Check the server address.',
              Colors.red
            ),
        };
        showSnackBarMessage(context, message: message, backgroundColor: color);
      }
```

- [ ] **Step 5: Verify**

Run: `cd mobile && flutter analyze`
Expected: clean. Manual check: wrong token in Settings → screens show "Invalid Token" with a key icon; server stopped → "Server Not Connected".

- [ ] **Step 6: Commit**

```bash
git add mobile/lib/services/settings_service.dart mobile/lib/widgets/disconnected_view.dart mobile/lib/screens/home_screen.dart mobile/lib/screens/system_screen.dart mobile/lib/screens/computers_screen.dart mobile/lib/screens/settings_screen.dart
git commit -m "feat(mobile): distinguish invalid token from offline server (UX-03)"
```

---

### Task 23: Readable snackbars, no raw exceptions (UX-05)

1-second snackbars are unreadable; several call sites dump raw Dart exceptions (`SocketException: ...`) at users.

**Files:**
- Modify: `mobile/lib/utils/snackbar_helper.dart` (full rewrite)
- Modify: `mobile/lib/screens/home_screen.dart:83`, `mobile/lib/screens/system_screen.dart:74`, `mobile/lib/screens/computers_screen.dart:86`, `mobile/lib/screens/settings_screen.dart:41,55-99,133`

- [ ] **Step 1: Rewrite the helper**

Replace the contents of `mobile/lib/utils/snackbar_helper.dart`:

```dart
import 'package:flutter/material.dart';

/// Errors stay long enough to read; confirmations get out of the way.
/// Hides any previous snackbar so rapid actions don't queue up.
void showSnackBarMessage(
  BuildContext context, {
  required String message,
  Color? backgroundColor,
  Duration? duration,
}) {
  final isError =
      backgroundColor == Colors.red || backgroundColor == Colors.orange;
  ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: backgroundColor,
        behavior: SnackBarBehavior.floating,
        duration: duration ??
            (isError
                ? const Duration(seconds: 4)
                : const Duration(seconds: 2)),
      ),
    );
}
```

- [ ] **Step 2: Replace raw-exception messages**

- `home_screen.dart:83` and `system_screen.dart:74`: `'Error loading data: $e'` → `'Could not load data from the server'`
- `computers_screen.dart:86`: `'Error loading computers: $e'` → `'Could not load computers from the server'`
- `settings_screen.dart:41`: `'Error loading settings: $e'` → `'Could not load settings'`
- `settings_screen.dart:133`: `'Connection test failed: $e'` → `'Connection test failed'`

- [ ] **Step 3: Stop `_saveSettings` from echoing exceptions**

The current code throws `Exception('Server address cannot be empty')` and shows `'$e'`, which renders as "Exception: Server address cannot be empty". Replace the whole `_saveSettings` in `mobile/lib/screens/settings_screen.dart`:

```dart
  Future<void> _saveSettings() async {
    final serverAddress = _serverAddressController.text.trim();
    final token = _tokenController.text.trim();

    if (serverAddress.isEmpty) {
      showSnackBarMessage(
        context,
        message: 'Server address cannot be empty',
        backgroundColor: Colors.orange,
      );
      return;
    }
    if (!serverAddress.startsWith('http://') &&
        !serverAddress.startsWith('https://')) {
      showSnackBarMessage(
        context,
        message: 'Server address must start with http:// or https://',
        backgroundColor: Colors.orange,
      );
      return;
    }

    setState(() {
      _isSaving = true;
    });

    try {
      await _settingsService.setServerAddress(serverAddress);
      await _settingsService.setToken(token.isEmpty ? null : token);

      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Settings saved',
          backgroundColor: Colors.green,
        );
      }
    } catch (e) {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Could not save settings',
          backgroundColor: Colors.red,
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _isSaving = false;
        });
      }
    }
  }
```

- [ ] **Step 4: Verify and commit**

Run: `cd mobile && flutter analyze`
Expected: clean.

```bash
git add mobile/lib/utils/snackbar_helper.dart mobile/lib/screens/
git commit -m "fix(mobile): readable snackbar durations, no raw exception text (UX-05)"
```

---

### Task 24: Confirm destructive toggles; mode change sends mode only (UX-04)

**Depends on server Task 8 being deployed first** — without it, a mode-only `PUT /status` would zero `enabled` (BUG-02). Centralizes the shield toggle (currently copy-pasted in three app bars) into one widget with a confirmation on disable, and adds a confirmation before switching to whitelist (which kills every unlisted process fleet-wide).

**Files:**
- Modify: `mobile/lib/services/settings_service.dart` (add `setServerMode`)
- Create: `mobile/lib/widgets/shield_button.dart`
- Modify: `mobile/lib/screens/home_screen.dart` (`_setMode` :314-333, delete `_toggleServerStatus` :301-312, app bar :483-491)
- Modify: `mobile/lib/screens/system_screen.dart` (delete `_toggleServerStatus` :247-258, app bar :282-290)
- Modify: `mobile/lib/screens/computers_screen.dart` (delete `_toggleServerStatus` :242-253, app bar :435-443)

- [ ] **Step 1: Add `setServerMode` to the service**

In `mobile/lib/services/settings_service.dart`, after `toggleServerStatus`:

```dart
  /// Changes only the server mode. Sends a mode-only payload so a stale
  /// cached `enabled` can never silently flip enforcement (requires the
  /// PUT /status partial-update fix from Task 8).
  Future<bool> setServerMode(String mode) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/status');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .put(uri, headers: headers, body: json.encode({'mode': mode}))
          .timeout(const Duration(seconds: 10));

      return response.statusCode == 200;
    } catch (e) {
      return false;
    } finally {
      client.close();
    }
  }
```

- [ ] **Step 2: Create the shared shield button**

Create `mobile/lib/widgets/shield_button.dart`:

```dart
import 'package:flutter/material.dart';

import '../services/settings_service.dart';
import '../utils/snackbar_helper.dart';

/// App-bar protection toggle shared by all screens. Disabling affects
/// every connected computer, so it asks for confirmation.
class ShieldButton extends StatelessWidget {
  ShieldButton({super.key});

  final SettingsService _settingsService = SettingsService();

  Future<void> _toggle(BuildContext context) async {
    final enabled = _settingsService.serverEnabled;

    if (enabled) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Disable Protection?'),
          content: const Text(
            'Agents on all computers will stop enforcing the rules.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            TextButton(
              onPressed: () => Navigator.of(context).pop(true),
              style: TextButton.styleFrom(foregroundColor: Colors.red),
              child: const Text('Disable'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    final success = await _settingsService.toggleServerStatus(!enabled);
    if (!success && context.mounted) {
      showSnackBarMessage(
        context,
        message: 'Failed to update server status',
        backgroundColor: Colors.red,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<bool>(
      valueListenable: _settingsService.serverEnabledNotifier,
      builder: (context, enabled, _) => IconButton(
        onPressed: () => _toggle(context),
        icon: Icon(
          enabled ? Icons.shield : Icons.shield_outlined,
          color: enabled ? Colors.green : Colors.orange,
        ),
        tooltip: enabled ? 'Disable protection' : 'Enable protection',
      ),
    );
  }
}
```

- [ ] **Step 3: Use it in all three screens**

In **each** of `home_screen.dart`, `system_screen.dart`, `computers_screen.dart`:

(a) Add the import:

```dart
import '../widgets/shield_button.dart';
```

(b) Delete the `_toggleServerStatus` method.

(c) Replace the app-bar block

```dart
          if (_isConnected)
            IconButton(
              onPressed: _toggleServerStatus,
              icon: Icon(
                _settingsService.serverEnabled ? Icons.shield : Icons.shield_outlined,
                color: _settingsService.serverEnabled ? Colors.green : Colors.orange,
              ),
              tooltip: _settingsService.serverEnabled ? 'Disable server' : 'Enable server',
            ),
```

with

```dart
          if (_isConnected) ShieldButton(),
```

(system_screen shows the button unconditionally today — keep the `if (_isConnected)` guard for consistency.)

- [ ] **Step 4: Confirm whitelist switches and stop resending `enabled`**

Replace `_setMode` in `mobile/lib/screens/home_screen.dart`:

```dart
  Future<void> _setMode(String mode) async {
    if (mode == _serverMode) return;

    if (mode == 'whitelist') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Switch to Whitelist?'),
          content: const Text(
            'In whitelist mode agents kill every process that is not on '
            'the list. Make sure the whitelist is complete first.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            TextButton(
              onPressed: () => Navigator.of(context).pop(true),
              style: TextButton.styleFrom(foregroundColor: Colors.red),
              child: const Text('Switch'),
            ),
          ],
        ),
      );
      if (confirmed != true) {
        setState(() {}); // snap the segmented button back
        return;
      }
    }

    final success = await _settingsService.setServerMode(mode);

    if (success) {
      setState(() {
        _serverMode = mode;
      });
    } else {
      if (mounted) {
        showSnackBarMessage(
          context,
          message: 'Failed to change mode',
          backgroundColor: Colors.red,
        );
      }
    }
  }
```

- [ ] **Step 5: Verify and commit**

Run: `cd mobile && flutter analyze`
Expected: clean. Manual check: tapping the green shield asks for confirmation; enabling doesn't; switching to whitelist asks; switching to blacklist doesn't; mode switch no longer flips the enabled state when another phone changed it meanwhile.

```bash
git add mobile/lib/services/settings_service.dart mobile/lib/widgets/shield_button.dart mobile/lib/screens/
git commit -m "feat(mobile): confirm destructive toggles, shared shield button, mode-only PUT (UX-04)"
```

---

### Task 25: Add-application sheet validates inline (UX-06)

Today the sheet pops **before** validation, so an empty/duplicate name shows a snackbar after the sheet is gone and the user must reopen and retype. Server error details (validation messages from Task 10) are discarded because the service returns only `bool`, and success shows no feedback.

**Files:**
- Modify: `mobile/lib/services/settings_service.dart:158-182` (`addBlockedApplication` returns `Future<String?>`)
- Modify: `mobile/lib/screens/home_screen.dart:95-195` (replace `_showAddAppSheet`, delete `_addApplication`)

- [ ] **Step 1: Surface the server's error message**

Replace `addBlockedApplication` in `mobile/lib/services/settings_service.dart`:

```dart
  /// Adds an application. Returns null on success, otherwise a
  /// human-readable error (the server's message when available).
  Future<String?> addBlockedApplication(String applicationName,
      {String mode = 'blacklist'}) async {
    final client = http.Client();
    try {
      final serverAddress = await getServerAddress();
      final uri = Uri.parse('$serverAddress/manage/applications');
      final headers = await _getHeaders(
        additionalHeaders: {'Content-Type': 'application/json'},
      );

      final response = await client
          .post(
            uri,
            headers: headers,
            body: json.encode({'name': applicationName, 'mode': mode}),
          )
          .timeout(const Duration(seconds: 10));

      if (response.statusCode == 201) return null;

      try {
        final body = json.decode(response.body);
        final error = body['error'];
        if (error is String && error.isNotEmpty) return error;
      } catch (_) {}
      return 'Failed to add application';
    } catch (e) {
      return 'Could not reach the server';
    } finally {
      client.close();
    }
  }
```

- [ ] **Step 2: Rework the bottom sheet**

In `mobile/lib/screens/home_screen.dart`, replace `_showAddAppSheet` AND delete the separate `_addApplication` method:

```dart
  void _showAddAppSheet() {
    _addAppController.clear();
    String? errorText;
    bool submitting = false;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (sheetContext) => StatefulBuilder(
        builder: (sheetContext, setSheetState) {
          Future<void> submit() async {
            final appName = _addAppController.text.trim();

            if (appName.isEmpty) {
              setSheetState(() => errorText = 'Enter an application name');
              return;
            }
            if (_allApps.any((app) =>
                app['name'] == appName && app['mode'] == _serverMode)) {
              setSheetState(
                  () => errorText = 'Already on the $_serverMode list');
              return;
            }

            setSheetState(() {
              submitting = true;
              errorText = null;
            });

            final error = await _settingsService.addBlockedApplication(
              appName,
              mode: _serverMode,
            );

            if (error != null) {
              setSheetState(() {
                submitting = false;
                errorText = error;
              });
              return;
            }

            final apps = await _settingsService.getBlockedApplications();
            if (mounted) {
              setState(() => _allApps = apps);
            }
            if (sheetContext.mounted) Navigator.pop(sheetContext);
            if (mounted) {
              showSnackBarMessage(
                context,
                message: 'Added "$appName"',
                backgroundColor: Colors.green,
              );
            }
          }

          return Padding(
            padding: EdgeInsets.only(
              left: 16,
              right: 16,
              top: 24,
              bottom: MediaQuery.of(sheetContext).viewInsets.bottom + 24,
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  'Add Application',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: 16),
                TextField(
                  controller: _addAppController,
                  autofocus: true,
                  enabled: !submitting,
                  decoration: InputDecoration(
                    hintText: 'Process name (e.g. firefox)',
                    prefixIcon: const Icon(Icons.app_blocking),
                    border: const OutlineInputBorder(),
                    errorText: errorText,
                  ),
                  onSubmitted: (_) => submit(),
                ),
                const SizedBox(height: 12),
                Text(
                  'Will be added as $_serverMode',
                  style: TextStyle(
                    fontSize: 13,
                    color: _modeColor(_serverMode),
                  ),
                ),
                const SizedBox(height: 16),
                SizedBox(
                  width: double.infinity,
                  child: ElevatedButton(
                    onPressed: submitting ? null : submit,
                    style: ElevatedButton.styleFrom(
                      backgroundColor: const Color(0xFF2D3748),
                      foregroundColor: Colors.white,
                      padding: const EdgeInsets.symmetric(vertical: 14),
                    ),
                    child: submitting
                        ? const SizedBox(
                            width: 18,
                            height: 18,
                            child: CircularProgressIndicator(
                              strokeWidth: 2,
                              color: Colors.white,
                            ),
                          )
                        : const Text('Add'),
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }
```

- [ ] **Step 3: Verify and commit**

Run: `cd mobile && flutter analyze`
Expected: clean (and no unused references to the deleted `_addApplication`). Manual check: empty name → inline error, sheet stays open; duplicate → inline error; server-side rejection (e.g. invalid characters after Task 10) → server's message inline; success → sheet closes, green "Added" snackbar.

```bash
git add mobile/lib/services/settings_service.dart mobile/lib/screens/home_screen.dart
git commit -m "feat(mobile): inline validation and server errors in add-app sheet (UX-06)"
```

---

### Task 26: Keep content on screen during refresh (UX-02)

Every `_loadData` sets `_isLoading = true`, replacing live content with a full-screen spinner — on every poll, settings change, and refresh tap. The fields already initialize to `true`, so simply not resetting them keeps the spinner for the first load only.

**Files:**
- Modify: `mobile/lib/screens/home_screen.dart:47-50`
- Modify: `mobile/lib/screens/system_screen.dart:43-46`
- Modify: `mobile/lib/screens/computers_screen.dart:51-54`

- [ ] **Step 1: Remove the leading spinner reset in each `_loadData`**

In each of the three files, delete this block at the top of `_loadData`:

```dart
    setState(() {
      _isLoading = true;
    });
```

and start the method like this (home_screen shown; the other two are identical except for their own bodies):

```dart
  Future<void> _loadData() async {
    // _isLoading starts true, so only the very first load shows the
    // full-screen spinner; later refreshes keep the current content.
    try {
```

The `finally` blocks that set `_isLoading = false` stay unchanged.

- [ ] **Step 2: Verify and commit**

Run: `cd mobile && flutter analyze`
Expected: clean. Manual check: tapping the app-bar refresh no longer blanks the list; pull-to-refresh shows only the RefreshIndicator.

```bash
git add mobile/lib/screens/home_screen.dart mobile/lib/screens/system_screen.dart mobile/lib/screens/computers_screen.dart
git commit -m "fix(mobile): full-screen spinner only on first load (UX-02)"
```

---

### Task 27: Interaction polish — token visibility, separate busy flags, ripple (UX-09)

**Files:**
- Modify: `mobile/lib/screens/settings_screen.dart` (fields :19-21, token field :207-216, buttons :218-262, `_testConnection` busy flag :113-115,138-144)
- Modify: `mobile/lib/screens/system_screen.dart:210-242` (tile ripple)

- [ ] **Step 1: Settings — show/hide token, independent Test/Save spinners**

Add fields next to `_isSaving` in `settings_screen.dart`:

```dart
  bool _isTesting = false;
  bool _obscureToken = true;
```

In `_testConnection`, replace both `_isSaving = true;` and `_isSaving = false;` assignments with `_isTesting = true;` / `_isTesting = false;` (the `setState` wrappers stay).

Replace the token `TextField`:

```dart
                            TextField(
                              controller: _tokenController,
                              decoration: InputDecoration(
                                hintText: 'Token',
                                prefixIcon: const Icon(Icons.security),
                                border: const OutlineInputBorder(),
                                suffixIcon: IconButton(
                                  icon: Icon(
                                    _obscureToken
                                        ? Icons.visibility
                                        : Icons.visibility_off,
                                  ),
                                  onPressed: () => setState(
                                      () => _obscureToken = !_obscureToken),
                                ),
                              ),
                              obscureText: _obscureToken,
                              enabled: !_isSaving && !_isTesting,
                            ),
```

Update the server-address field's `enabled:` to `!_isSaving && !_isTesting` as well.

Update the two buttons: Test gets `onPressed: (_isSaving || _isTesting) ? null : _testConnection` and its spinner condition becomes `_isTesting`; Save gets `onPressed: (_isSaving || _isTesting) ? null : _saveSettings` and keeps `_isSaving` for its spinner.

- [ ] **Step 2: System tiles get a ripple**

In `system_screen.dart` `_buildSystemGrid`, swap the `GestureDetector`-wrapping-`Card` for a `Card`-wrapping-`InkWell` (the inner `Padding`/`Column` content is unchanged):

```dart
        return Card(
          elevation: 2,
          clipBehavior: Clip.antiAlias,
          color: status ? Colors.green.shade600 : Colors.red.shade600,
          child: InkWell(
            onTap: () => _toggleSystemStatus(name, status),
            child: Padding(
              padding: const EdgeInsets.all(16.0),
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Icon(_getSystemIcon(name), size: 40, color: Colors.white),
                  const SizedBox(height: 10),
                  Text(
                    name[0].toUpperCase() + name.substring(1),
                    style: const TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                      color: Colors.white,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    status ? 'Enabled' : 'Disabled',
                    style: TextStyle(
                      fontSize: 12,
                      color: Colors.white.withValues(alpha: 0.8),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
```

- [ ] **Step 3: Verify and commit**

Run: `cd mobile && flutter analyze`
Expected: clean. Manual check: eye icon reveals the token; Test and Save spin independently; system tiles ripple on tap.

```bash
git add mobile/lib/screens/settings_screen.dart mobile/lib/screens/system_screen.dart
git commit -m "feat(mobile): token visibility toggle, separate busy flags, tile ripple (UX-09)"
```

---

### Task 28: Documentation sync for the mobile changes (repo rule)

**Files:**
- Modify: `CLAUDE.md` (Mobile architecture section, Build Commands)

- [ ] **Step 1: Update CLAUDE.md**

In the Mobile architecture bullet list, reflect: `SettingsService.checkConnection` returning a `ConnectionStatus` enum (connected/unauthorized/unreachable); shared widgets in `lib/widgets/` (`ShieldButton`, `DisconnectedView`); pure helpers in `lib/utils/time_utils.dart` (server timestamps are UTC-naive and must go through `parseServerTimestamp`); unit tests live in `mobile/test/`. Add `cd mobile && flutter test` to Build Commands. Update the "No automated tests exist yet" note to mention the server, agent, and mobile test suites added by this plan.

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: sync CLAUDE.md with mobile UI/UX changes"
```

---

## Out of scope — future design work (tracked in audit, not in this plan)

- **SEC-09 / SEC-12:** computer pairing protocol with per-agent secrets and token rotation. Requires API and agent-install UX design; interim risk reduced by Tasks 3 and 5.
- **SEC-11:** CORS restriction/removal — revisit if a browser frontend is ever added.
- **SEC-14:** move agent install path to `%ProgramFiles%\ProcSentinel\Agent` — needs an upgrade/migration path for deployed agents.
- **IMP-03:** uniform admin-action audit log; **IMP-05:** sync backoff/jitter; **IMP-06:** `/info` deriving real port/version; **SEC-15/16/17** hygiene items.
- **IMP-04:** Guardian↔ProcSentinel naming unification — rename touches binaries, services, and installers; do as its own change.
- **UX-01:** consolidate the three independent polling loops (main.dart nav timer, computers-screen timer, per-screen `_loadData`) into one shared repository/provider that screens subscribe to, paused when the app is backgrounded. Architectural change to mobile state management — design separately.
- **UX-07:** friendly computer names ("Kids' room PC") — needs a `name` column on `computers`, API support, and edit UI.
- **UX-10:** dark theme (`darkTheme` + `themeMode: ThemeMode.system`) and localization (`flutter_localizations` + ARB files) — l10n especially is a sweep of every hardcoded string.
- **UX-11:** the disabled-first sort on the Dashboard — product decision (intentional "needs attention first" or bug?), confirm with the owner before changing.
- **UX-12 / UX-09 remainder:** accessibility pass (Semantics labels on switches, non-color online indicator) and rethinking the inverted ON=blocked switch semantics on the Computers screen.
