package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// testLogger collects log lines so a test can assert on what the operator
// would have seen.
type testLogger struct{ lines []string }

func (l *testLogger) Infof(f string, a ...any)  { l.lines = append(l.lines, "INFO "+sprintf(f, a...)) }
func (l *testLogger) Warnf(f string, a ...any)  { l.lines = append(l.lines, "WARN "+sprintf(f, a...)) }
func (l *testLogger) Errorf(f string, a ...any) { l.lines = append(l.lines, "ERROR "+sprintf(f, a...)) }

func (l *testLogger) contains(s string) bool {
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }

func writeEnv(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(envFileName, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// fakeServer answers enroll and sync the way the real server does, recording
// what the agent sent.
type fakeServer struct {
	enrollStatus int
	syncStatus   int
	lastEnroll   map[string]any
	lastSync     map[string]any
	lastBearer   string
	syncs        int
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/enroll", func(w http.ResponseWriter, r *http.Request) {
		f.lastEnroll = map[string]any{}
		json.NewDecoder(r.Body).Decode(&f.lastEnroll)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.enrollStatus)
		switch f.enrollStatus {
		case http.StatusCreated:
			json.NewEncoder(w).Encode(map[string]string{"agent_token": "agent-token-1"})
		case http.StatusPaymentRequired:
			json.NewEncoder(w).Encode(map[string]string{"error": "Your plan does not cover another computer"})
		default:
			json.NewEncoder(w).Encode(map[string]string{"error": "This installer's token is no longer valid"})
		}
	})
	mux.HandleFunc("POST /agent/sync", func(w http.ResponseWriter, r *http.Request) {
		f.syncs++
		f.lastBearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.lastSync = map[string]any{}
		json.NewDecoder(r.Body).Decode(&f.lastSync)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.syncStatus)
		if f.syncStatus == http.StatusOK {
			json.NewEncoder(w).Encode(SyncResponse{
				Applications: []ClientApplication{{Name: "steam.exe", Mode: "blacklist"}},
				Mode:         "blacklist",
				Client:       []ClientEntry{{Name: "power", Status: true}},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
		}
	})
	return mux
}

func newTestAgent(t *testing.T, serverURL string) (*agent, *testLogger) {
	t.Helper()
	cfg, err := loadConfig(runModeConsole)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.ServerAddress != serverURL {
		t.Fatalf("SERVER_ADDRESS read as %q, want %q", cfg.ServerAddress, serverURL)
	}
	lg := &testLogger{}
	a := newAgent(cfg, lg)
	a.enrollRetry = time.Millisecond
	a.checkInterval = time.Millisecond
	return a, lg
}

// The whole first-run story: a fresh machine with only .env enrolls, saves
// its own token, drops the binding token from .env, and syncs with the new
// token.
func TestFirstRunEnrollsThenSyncsAndWipesTheBindingToken(t *testing.T) {
	t.Chdir(t.TempDir())
	fs := &fakeServer{enrollStatus: http.StatusCreated, syncStatus: http.StatusOK}
	ts := httptest.NewServer(fs.handler())
	defer ts.Close()

	writeEnv(t, "# keep me\r\nSERVER_ADDRESS="+ts.URL+"/\r\nBINDING_TOKEN=bt-secret\r\nCHECK_INTERVAL=5\r\n")
	a, lg := newTestAgent(t, ts.URL)
	a.loadCredentials()
	if a.creds != nil {
		t.Fatal("a fresh directory must not be enrolled")
	}

	if wait := a.step(); wait != 0 {
		t.Fatalf("after enrolling the agent should sync immediately, got wait %s; log: %v", wait, lg.lines)
	}
	if fs.lastEnroll["binding_token"] != "bt-secret" {
		t.Fatalf("enroll body: %v", fs.lastEnroll)
	}
	if fs.lastEnroll["machine_guid"] == "" || fs.lastEnroll["hostname"] == "" {
		t.Fatalf("enroll passport is missing identity: %v", fs.lastEnroll)
	}

	creds, err := loadCredentialsFile()
	if err != nil || creds == nil {
		t.Fatalf("credentials not saved: %v", err)
	}
	if creds.AgentToken != "agent-token-1" || creds.ServerAddress != ts.URL {
		t.Fatalf("saved credentials: %+v", creds)
	}

	env, _ := os.ReadFile(envFileName)
	if strings.Contains(string(env), "BINDING_TOKEN") {
		t.Fatalf("the binding token survived enrollment on disk:\n%s", env)
	}
	if !strings.Contains(string(env), "# keep me\r\n") || !strings.Contains(string(env), "CHECK_INTERVAL=5\r\n") {
		t.Fatalf("other .env lines were not preserved byte-for-byte:\n%q", env)
	}

	a.step()
	if fs.syncs != 1 || fs.lastBearer != "agent-token-1" {
		t.Fatalf("sync did not use the new token: syncs=%d bearer=%q", fs.syncs, fs.lastBearer)
	}
	rt, _ := fs.lastSync["runtime"].(map[string]any)
	if rt["mode"] != "console" || rt["agent_version"] != agentVersion {
		t.Fatalf("runtime telemetry: %v", fs.lastSync)
	}
	state := a.state.get()
	if state == nil || state.Mode != "blacklist" || len(state.Applications) != 1 {
		t.Fatalf("policy not applied: %+v", state)
	}
	if _, err := os.Stat("sync.json"); err != nil {
		t.Fatal("sync.json was not written after a successful sync")
	}
}

// No binding token and no credentials: the agent must say so, keep retrying,
// and keep enforcing whatever it last knew — never exit, never go blank.
func TestNotEnrolledWithoutBindingTokenKeepsTheCachedPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnv(t, "SERVER_ADDRESS=http://127.0.0.1:1\n")
	if err := saveSyncToFile(&SyncResponse{Mode: "whitelist", Applications: []ClientApplication{{Name: "notepad.exe"}}}); err != nil {
		t.Fatal(err)
	}
	a, lg := newTestAgent(t, "http://127.0.0.1:1")
	a.loadCachedPolicy()
	a.loadCredentials()

	if wait := a.step(); wait != a.enrollRetry {
		t.Fatalf("wait %s, want the enroll retry interval", wait)
	}
	if !lg.contains("no BINDING_TOKEN") {
		t.Fatalf("operator was not told why nothing happens: %v", lg.lines)
	}
	if st := a.state.get(); st == nil || st.Mode != "whitelist" {
		t.Fatalf("cached policy lost: %+v", st)
	}
}

func TestRejectedBindingTokenIsReportedDistinctly(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "no longer valid"},
		{http.StatusPaymentRequired, "Upgrade the plan"},
	} {
		fs := &fakeServer{enrollStatus: tc.status}
		ts := httptest.NewServer(fs.handler())
		writeEnv(t, "SERVER_ADDRESS="+ts.URL+"\nBINDING_TOKEN=bt\n")
		a, lg := newTestAgent(t, ts.URL)
		if wait := a.step(); wait != a.enrollRetry {
			t.Fatalf("%d: wait %s, want retry", tc.status, wait)
		}
		if !lg.contains(tc.want) {
			t.Fatalf("%d: message did not name the cause: %v", tc.status, lg.lines)
		}
		if _, err := os.Stat(credentialsFileName); err == nil {
			t.Fatalf("%d: credentials written after a refusal", tc.status)
		}
		ts.Close()
	}
}

// Revocation must fail secure: the last policy stays in force.
func TestRevokedTokenKeepsTheLastPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	fs := &fakeServer{syncStatus: http.StatusUnauthorized}
	ts := httptest.NewServer(fs.handler())
	defer ts.Close()

	writeEnv(t, "SERVER_ADDRESS="+ts.URL+"\n")
	if err := saveCredentialsFile(&credentials{AgentToken: "old", ServerAddress: ts.URL, EnrolledAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := saveSyncToFile(&SyncResponse{Mode: "blacklist", Applications: []ClientApplication{{Name: "steam.exe"}}}); err != nil {
		t.Fatal(err)
	}
	a, lg := newTestAgent(t, ts.URL)
	a.loadCachedPolicy()
	a.loadCredentials()
	if a.creds == nil {
		t.Fatal("saved credentials were not loaded")
	}

	a.step()
	if !lg.contains("no longer accepts") {
		t.Fatalf("revocation not reported: %v", lg.lines)
	}
	if st := a.state.get(); st == nil || len(st.Applications) != 1 {
		t.Fatalf("a revoked token cost the machine its policy: %+v", st)
	}
}

// Credentials minted by one server are not presented to another.
func TestCredentialsForAnotherServerAreIgnored(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnv(t, "SERVER_ADDRESS=http://new.example\n")
	if err := saveCredentialsFile(&credentials{AgentToken: "t", ServerAddress: "http://old.example"}); err != nil {
		t.Fatal(err)
	}
	a, lg := newTestAgent(t, "http://new.example")
	a.loadCredentials()
	if a.creds != nil {
		t.Fatal("credentials issued by another server were accepted")
	}
	if !lg.contains("not enrolled") {
		t.Fatalf("mismatch not explained: %v", lg.lines)
	}
}

func TestRemoveEnvKeyPreservesEverythingElse(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnv(t, "SERVER_ADDRESS=x\r\n# BINDING_TOKEN=commented stays\r\nBINDING_TOKEN=secret\r\n\r\nCHECK_INTERVAL=20")
	if err := removeEnvKey(envFileName, "BINDING_TOKEN"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(envFileName)
	want := "SERVER_ADDRESS=x\r\n# BINDING_TOKEN=commented stays\r\n\r\nCHECK_INTERVAL=20"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestEnvironmentOverridesTheFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeEnv(t, "SERVER_ADDRESS=http://file\nCHECK_INTERVAL=7\n")
	t.Setenv("SERVER_ADDRESS", "http://env/")
	cfg, err := loadConfig(runModeService)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerAddress != "http://env" {
		t.Fatalf("ServerAddress %q", cfg.ServerAddress)
	}
	if cfg.CheckInterval != 7*time.Second {
		t.Fatalf("CheckInterval %s", cfg.CheckInterval)
	}
}
