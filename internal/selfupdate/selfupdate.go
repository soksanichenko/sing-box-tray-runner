//go:build windows

// Package selfupdate replaces the currently running tray executable with a
// newly downloaded one. Windows allows renaming a mapped/executing image (it
// only forbids deleting the last link while mapped), so the swap is done via
// rename rather than direct overwrite.
package selfupdate

import (
	"fmt"
	"os"
)

// Apply renames the running executable at exePath aside (to exePath+".old",
// clobbering any leftover from a previous update) and moves newExePath into
// exePath's place. newExePath must be on the same volume as exePath, since
// Windows does not support renaming across drives.
func Apply(exePath, newExePath string) error {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("rename running exe aside: %w", err)
	}
	if err := os.Rename(newExePath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // best-effort rollback
		return fmt.Errorf("move new exe into place: %w", err)
	}
	return nil
}

// CleanupOld removes a leftover exePath+".old" from a previous self-update,
// if present. Call once at startup, before anything else touches exePath.
func CleanupOld(exePath string) {
	_ = os.Remove(exePath + ".old")
}
