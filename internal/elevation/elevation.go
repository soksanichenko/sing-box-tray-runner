//go:build windows

package elevation

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32        = windows.NewLazySystemDLL("shell32.dll")
	procShellExecW = shell32.NewProc("ShellExecuteW")
)

// tokenElevation mirrors Win32's TOKEN_ELEVATION struct.
type tokenElevation struct {
	TokenIsElevated uint32
}

// IsElevated returns true if the current process token has elevated privileges.
func IsElevated() bool {
	token := windows.GetCurrentProcessToken()

	var elev tokenElevation
	var size uint32
	err := windows.GetTokenInformation(
		token,
		windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elev)),
		uint32(unsafe.Sizeof(elev)),
		&size,
	)
	return err == nil && elev.TokenIsElevated != 0
}

// RelaunchAsAdmin re-launches the current executable with the "runas" verb,
// triggering a UAC prompt. extraArgs are appended to os.Args[1:].
// The caller should exit after this call.
func RelaunchAsAdmin(extraArgs ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)

	allArgs := append(os.Args[1:], extraArgs...)
	var argsPtr *uint16
	if len(allArgs) > 0 {
		argsPtr, _ = windows.UTF16PtrFromString(strings.Join(allArgs, " "))
	}

	// SW_SHOWNORMAL = 1; ShellExecute returns > 32 on success.
	ret, _, _ := procShellExecW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		0,
		1,
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW failed: %d", ret)
	}
	return nil
}
