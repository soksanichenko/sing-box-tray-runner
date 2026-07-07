//go:build windows

package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	taskName = "SingBoxTray"
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "SingBoxTray"
)

// Enable registers the executable to start on user logon. Non-elevated
// autostart (elevated=false) writes to the HKCU Run key, which a standard
// user always has write access to — unlike Task Scheduler, which some
// environments (group policy, EDR software) lock down even for a user's own
// tasks, causing schtasks to fail with "Access is denied". Elevated
// autostart (for TUN default mode) still needs Task Scheduler: with
// "/RL HIGHEST" it's the only mechanism that can launch with an
// administrator token at logon without an interactive UAC prompt — HKCU Run
// always launches at the user's normal integrity level.
// Whichever mechanism isn't being used is removed first, so switching
// between elevated and non-elevated autostart never leaves a stale
// duplicate entry (and therefore a duplicate launch) behind.
func Enable(elevated bool) error {
	if elevated {
		if registryEnabled() {
			_ = disableRegistry()
		}
		return enableTask()
	}
	if taskEnabled() {
		_ = disableTask()
	}
	return enableRegistry()
}

// Disable removes autostart via whichever mechanism is currently active.
func Disable() error {
	var errs []error
	if registryEnabled() {
		if err := disableRegistry(); err != nil {
			errs = append(errs, err)
		}
	}
	if taskEnabled() {
		if err := disableTask(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// IsEnabled reports whether autostart is active via either mechanism.
func IsEnabled() bool {
	return registryEnabled() || taskEnabled()
}

func enableRegistry() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry key: %w", err)
	}
	defer k.Close()
	// Wrap path in quotes so a space in the exe path doesn't get parsed as
	// an argument separator when Windows launches it at logon.
	if err := k.SetStringValue(runValue, `"`+exe+`"`); err != nil {
		return fmt.Errorf("set registry value: %w", err)
	}
	return nil
}

func disableRegistry() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open registry key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(runValue); err != nil {
		return fmt.Errorf("delete registry value: %w", err)
	}
	return nil
}

func registryEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func enableTask() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Wrap path in quotes so schtasks handles spaces in the path correctly.
	tr := `"` + exe + `"`

	args := []string{"/Create", "/TN", taskName, "/TR", tr, "/SC", "ONLOGON", "/RL", "HIGHEST", "/F"}
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Create: %w\n%s", err, out)
	}
	return nil
}

func disableTask() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks /Delete: %w\n%s", err, out)
	}
	return nil
}

func taskEnabled() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	return cmd.Run() == nil
}
