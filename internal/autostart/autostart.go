//go:build windows

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const taskName = "SingBoxTray"

// Enable creates (or replaces) a Task Scheduler task that launches the
// current executable on user logon. If elevated is true, the task runs
// with highest available privileges (required for TUN mode).
func Enable(elevated bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Wrap path in quotes so schtasks handles spaces in the path correctly.
	tr := `"` + exe + `"`

	args := []string{"/Create", "/TN", taskName, "/TR", tr, "/SC", "ONLOGON", "/F"}
	if elevated {
		args = append(args, "/RL", "HIGHEST")
	}

	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Create: %w\n%s", err, out)
	}
	return nil
}

// Disable removes the Task Scheduler task.
func Disable() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Delete: %w\n%s", err, out)
	}
	return nil
}

// IsEnabled returns true if the task exists in Task Scheduler.
func IsEnabled() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	return cmd.Run() == nil
}
