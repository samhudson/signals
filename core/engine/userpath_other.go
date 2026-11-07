//go:build !windows

package engine

import "os"

// SetUserPath: on Linux the install folder (~/.local/bin) is the usual place
// for your own programs; most distributions put it on the PATH once it exists.
func SetUserPath(on bool) (bool, error) { return false, nil }

// OnPath reports whether the install folder is on this process's PATH.
func OnPath() bool {
	_, missing := withDir(os.Getenv("PATH"), InstallDir(), ":", false)
	return !missing
}
