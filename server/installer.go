package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"server/internal/db"
)

// The installer is delivered as a ZIP whose only generated file is agent.env.
// Guardian.exe and the two agent binaries stay byte-identical for every
// customer, so they can be signed once and cached, and the server needs no Go
// toolchain to serve a download: it copies the reference archive's entries
// through verbatim (compressed bytes and all) and substitutes one small text
// file. See specs/2026-09-05-saas-design.md, "Installer".
const (
	// installerEnvName is the file substituted on every download. It must
	// match the name the console and the .bat installers read next to
	// themselves (tools/whitelist-gui: templateEnvPath).
	installerEnvName = "agent.env"

	// maxInstallerArchiveBytes bounds what this process will hold in memory
	// for one download. The shipped archive is two agent binaries and a GUI —
	// tens of megabytes — so a reference archive an order of magnitude larger
	// than that is a misconfiguration (a pointed-at backup, a wrong path),
	// and refusing it is better than a download that OOMs the service.
	maxInstallerArchiveBytes = 256 << 20 // 256 MiB

	// installerCheckInterval is the CHECK_INTERVAL written into the generated
	// agent.env, matching the shipped dist/agent/agent.env template.
	installerCheckInterval = "30"
)

// errNoInstallerArchive distinguishes "this server has no archive to serve"
// from "the archive is broken": both answer 503, but only the second is worth
// a stack of logging on every request.
var errNoInstallerArchive = errors.New("no installer archive is configured")

// installerArchiveFromEnv reads INSTALLER_ARCHIVE, the pre-built reference ZIP
// this server hands out. It is a path rather than a build step on purpose —
// the archive is produced by tools/mkinstaller at release time and the server
// only ever reads it.
func installerArchiveFromEnv() string {
	if p := strings.TrimSpace(os.Getenv("INSTALLER_ARCHIVE")); p != "" {
		return p
	}
	return defaultInstallerArchive
}

// defaultInstallerArchive is where install.sh and the compose deployment put
// the archive, relative to the server's working directory.
const defaultInstallerArchive = "installer/Guardian.zip"

// agentServerAddressFromEnv is the SERVER_ADDRESS written into a downloaded
// agent.env: the address the agent will talk to, which is this server's own
// public address. It defaults to the first CABINET_ORIGIN because in every
// deployment we ship, the cabinet and the agent API are the same origin behind
// the same proxy. AGENT_SERVER_ADDRESS overrides it for a split deployment.
func agentServerAddressFromEnv() (string, error) {
	if v := strings.TrimSpace(os.Getenv("AGENT_SERVER_ADDRESS")); v != "" {
		return strings.TrimRight(v, "/"), nil
	}
	origins, err := cabinetOriginsFromEnv()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(origins[0], "/"), nil
}

// handleDownloadInstaller streams a personalised installer archive.
//
// Manager-only, and account-scoped by construction: the binding token it mints
// is written inside the caller's own account scope, so the archive a manager
// downloads can only ever enrol machines into the account they were acting in.
func (s *Server) handleDownloadInstaller(w http.ResponseWriter, r *http.Request) {
	t, ok := mustTenant(w, r)
	if !ok {
		return
	}

	// The archive is opened and validated BEFORE a token is minted: a server
	// with no archive should not leave a trail of unused credentials behind
	// every attempt to download one.
	ref, closeRef, err := s.openInstallerArchive()
	if err != nil {
		if errors.Is(err, errNoInstallerArchive) {
			slog.Warn("installer download refused: no archive configured", "path", s.installerArchive)
		} else {
			slog.Error("installer archive unusable", "path", s.installerArchive, "error", err)
		}
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Error: "No installer is available from this server yet"})
		return
	}
	defer closeRef()

	serverAddress, err := agentServerAddressFromEnv()
	if err != nil {
		slog.Error("installer download has no server address to write", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Error: "No installer is available from this server yet"})
		return
	}

	var (
		plain       string
		accountName string
	)
	err = s.inAccount(r.Context(), t.AccountID, func(tx pgx.Tx) error {
		q := db.New(tx)
		account, err := q.GetAccount(r.Context(), t.AccountID)
		if err != nil {
			return err
		}
		accountName = account.Name

		var hash string
		plain, hash = newToken()
		if _, err := q.CreateBindingToken(r.Context(), db.CreateBindingTokenParams{
			AccountID: t.AccountID, TokenHash: hash,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(bindingTokenTTL), Valid: true},
		}); err != nil {
			return err
		}
		// The same event type a hand-minted token records, with the source in
		// the payload: to an operator reading the feed, a token that appeared
		// because somebody downloaded an installer is the same fact as one
		// minted from the API, and the distinction belongs in the detail.
		return s.recordEvent(r.Context(), tx, eventInput{
			AccountID: t.AccountID, Type: "binding_token.created",
			Payload: map[string]any{"source": "installer"},
		})
	})
	if err != nil {
		slog.Error("mint installer binding token", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "Could not build an installer"})
		return
	}

	archive, err := buildInstallerArchive(ref, installerEnv(serverAddress, plain))
	if err != nil {
		slog.Error("build installer archive", "error", err)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error: "Could not build an installer"})
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", installerFileName(accountName)))
	w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
	// The archive carries a live credential. It is not something a proxy or a
	// browser should keep a copy of after the download.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(archive); err != nil {
		slog.Warn("installer download interrupted", "error", err)
	}
}

// openInstallerArchive opens the reference archive and checks it is the thing
// we think it is. A ZIP that parses but holds no agent binary would download
// perfectly and install nothing, which is a far worse failure than a 503.
func (s *Server) openInstallerArchive() (*zip.Reader, func(), error) {
	path := s.installerArchive
	if path == "" {
		return nil, nil, errNoInstallerArchive
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("%w: %s is not there", errNoInstallerArchive, path)
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if info.Size() > maxInstallerArchiveBytes {
		f.Close()
		return nil, nil, fmt.Errorf("reference archive is %d bytes, over the %d cap",
			info.Size(), maxInstallerArchiveBytes)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !hasAgentBinary(zr) {
		f.Close()
		return nil, nil, errors.New("reference archive contains no agent executable")
	}
	return zr, func() { f.Close() }, nil
}

// hasAgentBinary looks for the payload the console installs. The names are the
// ones agentExeName produces in tools/whitelist-gui.
func hasAgentBinary(zr *zip.Reader) bool {
	for _, f := range zr.File {
		name := strings.ToLower(zipEntryName(f.Name))
		if strings.HasSuffix(name, "procsentinel-agent32.exe") ||
			strings.HasSuffix(name, "procsentinel-agent64.exe") {
			return true
		}
	}
	return false
}

// buildInstallerArchive copies every entry of the reference archive through
// unchanged and writes env in place of agent.env.
//
// The copy is raw: the entries keep the compressed bytes they already had, so
// serving a download costs no compression work no matter how many machines a
// customer is installing on. Only the generated file is compressed here.
func buildInstallerArchive(ref *zip.Reader, env string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, f := range ref.File {
		if strings.EqualFold(zipEntryName(f.Name), installerEnvName) {
			// Replaced below. A reference archive carrying a token of its own
			// would otherwise ship it to every customer.
			continue
		}
		rc, err := f.OpenRaw()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		header := f.FileHeader
		dst, err := zw.CreateRaw(&header)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", f.Name, err)
		}
		if _, err := io.Copy(dst, rc); err != nil {
			return nil, fmt.Errorf("copy %s: %w", f.Name, err)
		}
	}

	dst, err := zw.Create(installerEnvName)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(dst, env); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// installerEnv is the one generated file in the archive. The key order is the
// one tools/whitelist-gui writes (knownEnvOrder) and dist/agent/agent.env
// templates, so a customer who opens the file sees what the console would have
// written — the whole point of shipping configuration as text.
func installerEnv(serverAddress, bindingToken string) string {
	return "SERVER_ADDRESS=" + serverAddress + "\r\n" +
		"BINDING_TOKEN=" + bindingToken + "\r\n" +
		"CHECK_INTERVAL=" + installerCheckInterval + "\r\n"
}

// zipEntryName normalises a ZIP entry name for comparison. ZIP always uses
// forward slashes, but archives in the wild carry "./" prefixes and the
// occasional backslash.
func zipEntryName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	return name
}

// installerFileName derives Guardian-<account>.zip from the account's name.
// The name is a customer's free text, so everything outside a conservative
// ASCII set is dropped rather than escaped: this string ends up in a
// Content-Disposition header and then in a filename on somebody's desktop.
func installerFileName(accountName string) string {
	var b strings.Builder
	lastDash := true // leading dashes are suppressed
	for _, r := range accountName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "Guardian.zip"
	}
	return "Guardian-" + slug + ".zip"
}
