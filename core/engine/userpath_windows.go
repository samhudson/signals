//go:build windows

package engine

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const environmentKey = `Environment`

// SetUserPath adds the install folder to (or removes it from) the user's PATH,
// so `signals` works in any new terminal. No admin needed.
func SetUserPath(on bool) (bool, error) {
	changed, err := setPathIn(environmentKey, InstallDir(), on)
	if changed {
		announceEnvironment()
	}
	return changed, err
}

// OnPath reports whether the install folder is on this process's PATH.
func OnPath() bool {
	_, missing := withDir(os.Getenv("PATH"), InstallDir(), ";", true)
	return !missing
}

func setPathIn(keyPath, dir string, on bool) (bool, error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	current, kind, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		current, kind, err = "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		return false, err
	}
	var next string
	var changed bool
	if on {
		next, changed = withDir(current, dir, ";", true)
	} else {
		next, changed = withoutDir(current, dir, ";", true)
	}
	if !changed {
		return false, nil
	}
	if kind == registry.SZ {
		return true, k.SetStringValue("Path", next)
	}
	return true, k.SetExpandStringValue("Path", next)
}

// announceEnvironment tells Explorer the environment changed, so terminals
// opened from now on see the new PATH without signing out.
func announceEnvironment() {
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	env, _ := syscall.UTF16PtrFromString("Environment")
	var result uintptr
	syscall.NewLazyDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(
		hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 1000, uintptr(unsafe.Pointer(&result)))
}
