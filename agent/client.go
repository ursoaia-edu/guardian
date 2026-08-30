package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// credentialsFileName holds this machine's own agent token once it has
// enrolled. It sits next to .env but is deliberately not part of it: .env is
// configuration a person edits and the installer rewrites, this is a secret
// the agent owns.
const credentialsFileName = "agent_credentials.json"

type credentials struct {
	AgentToken    string    `json:"agent_token"`
	MachineGUID   string    `json:"machine_guid"`
	ServerAddress string    `json:"server_address"`
	EnrolledAt    time.Time `json:"enrolled_at"`
}

// loadCredentialsFile returns nil, nil when the machine has never enrolled.
func loadCredentialsFile() (*credentials, error) {
	data, err := os.ReadFile(credentialsFileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if c.AgentToken == "" {
		return nil, errors.New("no agent_token in the file")
	}
	return &c, nil
}

func saveCredentialsFile(c *credentials) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(credentialsFileName, data, 0600)
}

// writeFileAtomic writes to a sibling temp file and renames it into place, so
// a crash mid-write cannot leave a half-written credential or .env behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// readEnvFile parses KEY=VALUE lines. Blank lines and # comments are skipped;
// anything more elaborate is not supported, and the console's editor writes
// nothing more elaborate.
func readEnvFile(path string) (map[string]string, error) {
	vals := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vals, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return vals, sc.Err()
}

// removeEnvKey deletes every KEY=... line from the file and leaves every other
// byte — comments, blank lines, line endings, other keys — exactly as it was.
func removeEnvKey(path, key string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.SplitAfter(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if k, _, ok := strings.Cut(trimmed, "="); ok && !strings.HasPrefix(trimmed, "#") && strings.TrimSpace(k) == key {
			continue
		}
		out = append(out, line)
	}
	return writeFileAtomic(path, []byte(strings.Join(out, "")), 0644)
}

func randomGUID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "generated-" + hex.EncodeToString(raw)
}

// passport is the machine's description sent once, at enrollment.
type passport struct {
	MachineGUID  string
	Hostname     string
	OSName       string
	OSBuild      string
	Arch         string
	AgentVersion string
	Hardware     map[string]any
}

// Sentinel errors the run loop branches on. Each wraps the server's own
// message, so a log line carries both the category and what the server said.
var (
	errBindingTokenRejected = errors.New("binding token rejected")
	errPlanLimit            = errors.New("plan limit reached")
	errUnauthorized         = errors.New("agent token rejected")
)

type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string) *apiClient {
	return &apiClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// enroll trades the installer's binding token for this machine's own token.
func (c *apiClient) enroll(bindingToken string, p passport) (string, error) {
	body := map[string]any{
		"binding_token": bindingToken,
		"machine_guid":  p.MachineGUID,
		"hostname":      p.Hostname,
		"os_name":       p.OSName,
		"os_build":      p.OSBuild,
		"arch":          p.Arch,
		"agent_version": p.AgentVersion,
		"hardware":      p.Hardware,
	}
	status, raw, err := c.post("/agent/enroll", "", body)
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusCreated:
		var out struct {
			AgentToken string `json:"agent_token"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.AgentToken == "" {
			return "", fmt.Errorf("server answered 201 without an agent token")
		}
		return out.AgentToken, nil
	case http.StatusUnauthorized:
		return "", fmt.Errorf("%w: %s", errBindingTokenRejected, serverMessage(raw))
	case http.StatusPaymentRequired:
		return "", fmt.Errorf("%w: %s", errPlanLimit, serverMessage(raw))
	default:
		return "", fmt.Errorf("server returned %d: %s", status, serverMessage(raw))
	}
}

// sync fetches the current policy, reporting the given telemetry and the
// batch of processes this machine has killed since the last acknowledged sync.
func (c *apiClient) sync(agentToken string, runtime map[string]any, batchID string, blocked []blockedEntry) (*SyncResponse, error) {
	body := map[string]any{"runtime": runtime}
	// Omitted rather than sent empty: the body stays what the pre-batch server
	// parses when there is nothing to report, so an older server sees exactly
	// the request it always saw.
	if batchID != "" && len(blocked) > 0 {
		body["batch_id"] = batchID
		body["blocked"] = blocked
	}
	status, raw, err := c.post("/agent/sync", agentToken, body)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		var resp SyncResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &resp, nil
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", errUnauthorized, serverMessage(raw))
	default:
		return nil, fmt.Errorf("server returned %d: %s", status, serverMessage(raw))
	}
}

func (c *apiClient) post(path, bearer string, body any) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "procsentinel-agent/"+agentVersion)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("connect to server: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// serverMessage pulls the human-readable error the server puts in every
// non-2xx body, falling back to the raw body when it is not that shape.
func serverMessage(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(raw))
}
