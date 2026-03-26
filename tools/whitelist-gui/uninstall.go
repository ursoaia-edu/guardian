package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// preservedFiles are the files worth keeping when the agent is removed: a
// hand-curated whitelist is real work, and the .env carries the server address
// and token.
var preservedFiles = []string{whitelistFileName, envFileName}

// validateDeletionTarget guards the recursive delete. The directory comes from
// the service's ImagePath, a registry value any administrator -- or anything
// running with administrator rights -- can rewrite. Deleting whatever it points
// at unchecked is not acceptable.
func validateDeletionTarget(dir string) error {
	if dir == "" {
		return fmt.Errorf("the installation folder is unknown")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %v", err)
	}
	clean := filepath.Clean(abs)

	if isProtectedDir(clean) {
		return fmt.Errorf("refused: %s is a system folder and must not be deleted", clean)
	}

	if pathDepth(clean) < 3 {
		return fmt.Errorf("refused: %s is too close to the drive root", clean)
	}

	if !containsAgentExe(clean) {
		return fmt.Errorf("refused: %s contains neither procsentinel-agent32.exe nor procsentinel-agent64.exe; "+
			"it does not look like the agent folder", clean)
	}

	return nil
}

// protectedDirs are directories that must never be removed, whatever the
// registry claims.
func protectedDirs() []string {
	var dirs []string
	add := func(p string) {
		if p != "" {
			dirs = append(dirs, filepath.Clean(p))
		}
	}

	winDir := os.Getenv("WINDIR")
	if winDir == "" {
		winDir = `C:\Windows`
	}
	add(winDir)
	add(filepath.Join(winDir, "System32"))
	add(filepath.Join(winDir, "SysWOW64"))
	add(os.Getenv("ProgramFiles"))
	add(os.Getenv("ProgramFiles(x86)"))
	add(os.Getenv("ProgramData"))
	add(os.Getenv("USERPROFILE"))
	add(os.Getenv("SystemDrive") + `\`)

	return dirs
}

func isProtectedDir(dir string) bool {
	if isDriveRoot(dir) {
		return true
	}
	for _, p := range protectedDirs() {
		if strings.EqualFold(dir, p) {
			return true
		}
	}
	return false
}

func isDriveRoot(dir string) bool {
	vol := filepath.VolumeName(dir)
	if vol == "" {
		return false
	}
	rest := strings.Trim(dir[len(vol):], `\/`)
	return rest == ""
}

// pathDepth counts the components below the volume: C:\a\b\c is 3.
func pathDepth(dir string) int {
	vol := filepath.VolumeName(dir)
	rest := strings.Trim(dir[len(vol):], `\/`)
	if rest == "" {
		return 0
	}
	return len(strings.Split(rest, string(filepath.Separator)))
}

func containsAgentExe(dir string) bool {
	for _, name := range []string{"procsentinel-agent64.exe", "procsentinel-agent32.exe"} {
		if _, err := os.Stat(ioPath(filepath.Join(dir, name))); err == nil {
			return true
		}
	}
	return false
}

// countFiles reports how many files the confirmation dialog is about to delete.
func countFiles(dir string) int {
	count := 0
	_ = filepath.Walk(ioPath(dir), func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// performUninstall stops the agent, deregisters it and removes its directory.
// Unlike Uninstall-Agent*.ps1, the configuration is preserved by default.
func performUninstall(dir string, keepConfig bool, progress func(string)) (backup string, err error) {
	if err := validateDeletionTarget(dir); err != nil {
		return "", err
	}

	if serviceIsRegistered() {
		progress("stopping the service")
		if err := stopService(); err != nil {
			return "", fmt.Errorf("could not stop the service: %v", err)
		}

		progress("deregistering the service")
		if err := removeExistingService(dir); err != nil {
			return "", err
		}
	}

	if keepConfig {
		progress("preserving the configuration")
		backup, err = backupPreservedFiles(dir)
		if err != nil {
			return "", err
		}
	}

	progress("deleting files")
	if err := os.RemoveAll(ioPath(dir)); err != nil {
		return backup, fmt.Errorf("could not delete the folder: %v", err)
	}

	return backup, nil
}

// backupPreservedFiles copies the files worth keeping somewhere outside the
// directory that is about to be deleted.
func backupPreservedFiles(dir string) (string, error) {
	var present []string
	for _, name := range preservedFiles {
		if _, err := os.Stat(ioPath(filepath.Join(dir, name))); err == nil {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		return "", nil
	}

	backup, err := newBackupDir()
	if err != nil {
		return "", err
	}

	for _, name := range present {
		if err := copyFile(filepath.Join(dir, name), filepath.Join(backup, name)); err != nil {
			return "", fmt.Errorf("could not preserve %s: %v", name, err)
		}
	}
	return backup, nil
}
