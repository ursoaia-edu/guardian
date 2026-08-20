package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type fileFacts struct {
	Path string
	Size int64
	Mod  time.Time
	Hash string
}

func inspectFile(path string) (fileFacts, error) {
	info, err := os.Stat(ioPath(path))
	if err != nil {
		return fileFacts{}, err
	}

	hash, err := fileSHA256(path)
	if err != nil {
		return fileFacts{}, err
	}

	return fileFacts{Path: path, Size: info.Size(), Mod: info.ModTime(), Hash: hash}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(ioPath(path))
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ShortHash is enough to tell two builds apart on screen.
func (f fileFacts) ShortHash() string {
	if len(f.Hash) < 12 {
		return f.Hash
	}
	return f.Hash[:12]
}

func (f fileFacts) Describe() string {
	return fmt.Sprintf("%.1f MB, %s, sha256 %s",
		float64(f.Size)/(1024*1024), f.Mod.Format("2006-01-02 15:04"), f.ShortHash())
}

type updateInfo struct {
	Source    fileFacts
	Installed fileFacts

	// Identical means the candidate is byte-for-byte what is already installed,
	// so there is nothing to do and no reason to interrupt the service.
	Identical bool

	// ArchChange means the candidate has a different filename than the
	// registered executable -- a 32/64-bit switch. Overwriting in place would
	// leave ImagePath pointing at a name that no longer exists, so this case
	// must go through the full install flow instead.
	ArchChange bool
}

func inspectUpdate(sourceExe, installedExe string) (*updateInfo, error) {
	src, err := inspectFile(sourceExe)
	if err != nil {
		return nil, fmt.Errorf("new agent executable is not readable: %v", err)
	}

	inst, err := inspectFile(installedExe)
	if err != nil {
		return nil, fmt.Errorf("installed agent executable is not readable: %v", err)
	}

	return &updateInfo{
		Source:     src,
		Installed:  inst,
		Identical:  src.Hash == inst.Hash,
		ArchChange: !strings.EqualFold(filepath.Base(sourceExe), filepath.Base(installedExe)),
	}, nil
}

// performUpdate replaces the installed executable in place. The service
// registration is deliberately left alone: ImagePath still points at the same
// file, so re-registering would only risk losing the event-log source.
func performUpdate(info *updateInfo, progress func(string)) error {
	if info.Identical {
		return fmt.Errorf("the installed agent is already identical to the new one")
	}
	if info.ArchChange {
		return fmt.Errorf("agent architecture changes (%s -> %s); a full reinstall is required",
			filepath.Base(info.Installed.Path), filepath.Base(info.Source.Path))
	}

	backup, err := newBackupDir()
	if err != nil {
		return err
	}

	progress("backing up the current agent")
	if err := copyFile(info.Installed.Path, filepath.Join(backup, filepath.Base(info.Installed.Path))); err != nil {
		return fmt.Errorf("could not write the backup: %v", err)
	}

	// The running executable is locked; it cannot be overwritten in place.
	progress("stopping the service")
	if err := stopService(); err != nil {
		return fmt.Errorf("could not stop the service: %v", err)
	}

	progress("replacing the executable")
	if err := copyFile(info.Source.Path, info.Installed.Path); err != nil {
		// Leave the service stopped rather than starting a half-written binary.
		return fmt.Errorf("could not replace the agent (backup kept in %s): %v", backup, err)
	}

	progress("starting the service")
	if err := startService(); err != nil {
		return fmt.Errorf("the agent was replaced but the service did not start (backup in %s): %v", backup, err)
	}

	return nil
}

// newBackupDir creates a timestamped directory under ProgramData, outside any
// directory an uninstall might delete.
func newBackupDir() (string, error) {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}

	dir := filepath.Join(root, "ProcSentinel", "backup", time.Now().Format("2006-01-02_150405"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("could not create the backup folder: %v", err)
	}
	return dir, nil
}
