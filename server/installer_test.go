package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// referenceArchive writes a stand-in for the release ZIP: the entries the real
// one carries, including an agent.env that must NOT survive the download.
func referenceArchive(t *testing.T, withEnv bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Guardian.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"Guardian.exe":                       "MZ-not-really",
		"bin/agent/procsentinel-agent32.exe": "MZ-32",
		"bin/agent/procsentinel-agent64.exe": "MZ-64",
		"README.txt":                         "three steps",
	}
	if withEnv {
		entries[installerEnvName] = "SERVER_ADDRESS=https://template.example\r\nBINDING_TOKEN=template_placeholder\r\n"
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return path
}

// downloadedEntries reads the served archive back into a name → content map.
func downloadedEntries(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	body := rr.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("the download is not a readable ZIP: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(content)
	}
	return out
}

func TestInstallerDownloadCarriesAWorkingBindingToken(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://guardian.example.com")
	s := &Server{pool: testPool(t), installerArchive: referenceArchive(t, true)}
	c := registerAndLogin(t, s, "parent@example.com")

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/installer", nil, c)
	if rr.Code != 200 {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("an archive holding a live credential must not be cached, got %q", got)
	}

	entries := downloadedEntries(t, rr)
	for _, want := range []string{"Guardian.exe", "bin/agent/procsentinel-agent32.exe",
		"bin/agent/procsentinel-agent64.exe", "README.txt", installerEnvName} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("the archive is missing %s; it has %v", want, keysOf(entries))
		}
	}
	if entries["bin/agent/procsentinel-agent64.exe"] != "MZ-64" {
		t.Error("a copied entry did not survive the rebuild intact")
	}

	env := entries[installerEnvName]
	if strings.Contains(env, "template_placeholder") {
		t.Fatal("the reference archive's own agent.env was shipped to a customer")
	}
	if !strings.Contains(env, "SERVER_ADDRESS=https://guardian.example.com\r\n") {
		t.Errorf("agent.env does not point at this server: %q", env)
	}
	if !strings.Contains(env, "CHECK_INTERVAL=") {
		t.Errorf("agent.env is missing CHECK_INTERVAL: %q", env)
	}

	// The point of the whole endpoint: the token in that file enrols a machine.
	token := ""
	for _, line := range strings.Split(env, "\r\n") {
		if strings.HasPrefix(line, "BINDING_TOKEN=") {
			token = strings.TrimPrefix(line, "BINDING_TOKEN=")
		}
	}
	if token == "" {
		t.Fatalf("agent.env carries no binding token: %q", env)
	}
	if code, agentToken := enroll(t, s, token, "guid-installer", "PC-FROM-ZIP"); code != 201 || agentToken == "" {
		t.Fatalf("the downloaded token did not enrol a machine: %d", code)
	}
}

// The archive is the account's credential in a box: a token minted for one
// account must not reach another's fleet.
func TestInstallerTokenEnrolsIntoTheDownloadersAccount(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://guardian.example.com")
	s := &Server{pool: testPool(t), installerArchive: referenceArchive(t, false)}
	h := s.setupRoutes()
	ca := registerAndLogin(t, s, "a@example.com")
	cb := registerAndLogin(t, s, "b@example.com")

	rr := doJSON(t, h, "GET", "/api/v1/installer", nil, cb)
	if rr.Code != 200 {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}
	env := downloadedEntries(t, rr)[installerEnvName]
	var token string
	for _, line := range strings.Split(env, "\r\n") {
		token = strings.TrimPrefix(line, "BINDING_TOKEN=")
		if token != line {
			break
		}
	}
	if code, _ := enroll(t, s, token, "guid-b-zip", "PC-B-ZIP"); code != 201 {
		t.Fatalf("enroll with B's installer: %d", code)
	}

	var listA struct {
		Computers []struct {
			Hostname string `json:"hostname"`
		} `json:"computers"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/computers", nil, ca), &listA)
	if len(listA.Computers) != 0 {
		t.Fatalf("account A sees a machine enrolled with account B's installer: %+v", listA.Computers)
	}
}

func TestInstallerDownloadIsRecordedInTheEventFeed(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://guardian.example.com")
	s := &Server{pool: testPool(t), installerArchive: referenceArchive(t, false)}
	h := s.setupRoutes()
	c := registerAndLogin(t, s, "parent@example.com")

	if rr := doJSON(t, h, "GET", "/api/v1/installer", nil, c); rr.Code != 200 {
		t.Fatalf("download: %d %s", rr.Code, rr.Body.String())
	}

	var feed struct {
		Events []struct {
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
		} `json:"events"`
	}
	decodeInto(t, doJSON(t, h, "GET", "/api/v1/events", nil, c), &feed)
	for _, e := range feed.Events {
		if e.Type == "binding_token.created" && e.Payload["source"] == "installer" {
			return
		}
	}
	t.Fatalf("no binding_token.created event from the installer: %+v", feed.Events)
}

// A server with no archive is a server that cannot hand out an installer. It
// must say so rather than serve an empty ZIP — and it must not leave a minted
// credential behind for a download that never happened.
func TestInstallerWithoutAnArchiveIs503AndMintsNothing(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://guardian.example.com")
	s := &Server{pool: testPool(t)}
	c := registerAndLogin(t, s, "parent@example.com")

	rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/installer", nil, c)
	if rr.Code != 503 {
		t.Fatalf("expected 503 with no archive, got %d %s", rr.Code, rr.Body.String())
	}

	var tokens int
	if err := observe(t).QueryRow(t.Context(),
		`SELECT count(*) FROM binding_tokens`).Scan(&tokens); err != nil {
		t.Fatalf("count binding tokens: %v", err)
	}
	if tokens != 0 {
		t.Fatalf("a failed download minted %d binding tokens", tokens)
	}
}

// A ZIP that parses but holds no agent would install nothing at all, which is
// worse than an honest refusal.
func TestInstallerRefusesAnArchiveWithNoAgent(t *testing.T) {
	t.Setenv("CABINET_ORIGIN", "https://guardian.example.com")
	path := filepath.Join(t.TempDir(), "Guardian.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("README.txt")
	io.WriteString(w, "nothing useful here")
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	s := &Server{pool: testPool(t), installerArchive: path}
	c := registerAndLogin(t, s, "parent@example.com")
	if rr := doJSON(t, s.setupRoutes(), "GET", "/api/v1/installer", nil, c); rr.Code != 503 {
		t.Fatalf("expected 503 for an agentless archive, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestInstallerFileNameIsSafeForAHeader(t *testing.T) {
	cases := map[string]string{
		"The Smiths":            "Guardian-The-Smiths.zip",
		"  ":                    "Guardian.zip",
		"Семья":                 "Guardian.zip",
		`a"; rm -rf /`:          "Guardian-a-rm-rf.zip",
		"Kids Room #2":          "Guardian-Kids-Room-2.zip",
		strings.Repeat("x", 80): "Guardian-" + strings.Repeat("x", 40) + ".zip",
	}
	for in, want := range cases {
		if got := installerFileName(in); got != want {
			t.Errorf("installerFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentServerAddressFallsBackToTheCabinetOrigin(t *testing.T) {
	t.Setenv("AGENT_SERVER_ADDRESS", "")
	t.Setenv("CABINET_ORIGIN", "https://app.example.com/,https://other.example.com")
	got, err := agentServerAddressFromEnv()
	if err != nil {
		t.Fatalf("agentServerAddressFromEnv: %v", err)
	}
	if got != "https://app.example.com" {
		t.Errorf("fallback = %q, want the first cabinet origin without its slash", got)
	}

	t.Setenv("AGENT_SERVER_ADDRESS", "https://agents.example.com/")
	got, err = agentServerAddressFromEnv()
	if err != nil {
		t.Fatalf("agentServerAddressFromEnv: %v", err)
	}
	if got != "https://agents.example.com" {
		t.Errorf("override = %q", got)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
