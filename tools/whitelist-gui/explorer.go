package main

import (
	"fmt"
	"os"
	"os/exec"
)

// openInExplorer opens the agent folder in Explorer. explorer.exe is known to
// return a non-zero exit code even on success, so only a failure to start the
// process is reported.
// Explorer is a 64-bit process and cannot resolve the Sysnative alias, so it
// is handed the canonical path even though we probe it through ioPath.
func openInExplorer(dir string) error {
	if _, err := os.Stat(ioPath(dir)); err != nil {
		return fmt.Errorf("folder is not accessible: %v", err)
	}
	return exec.Command("explorer.exe", dir).Start()
}
