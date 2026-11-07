package engine

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// InstallDir is where `signals install` keeps the program, so agents' hooks
// keep working after the download is deleted: ~/.local/bin on Linux,
// %LOCALAPPDATA%\Programs\Signals on Windows.
func InstallDir() string {
	if v := os.Getenv("SIGNALS_INSTALL_DIR"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "Programs", "Signals")
		}
		return filepath.Join(Home(), "AppData", "Local", "Programs", "Signals")
	}
	return filepath.Join(Home(), ".local", "bin")
}

func exeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// Place copies this program (and, on Windows, the tray app beside it) into
// InstallDir, returning the installed program's path.
func Place() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, _ = filepath.EvalSymlinks(self)
	dir := InstallDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, exeName("signals"))
	if err := copyFile(self, dst); err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		tray := filepath.Join(filepath.Dir(self), exeName("signals-tray"))
		if _, err := os.Stat(tray); err == nil {
			copyFile(tray, filepath.Join(dir, exeName("signals-tray")))
		}
	}
	return dst, nil
}

// TrayProgram is the program and arguments that show the tray icon.
func TrayProgram() (string, []string) {
	if runtime.GOOS == "windows" {
		return filepath.Join(InstallDir(), exeName("signals-tray")), nil
	}
	return filepath.Join(InstallDir(), exeName("signals")), []string{"tray"}
}

func copyFile(src, dst string) error {
	if a, err := filepath.Abs(src); err == nil {
		if b, err := filepath.Abs(dst); err == nil && a == b {
			return nil
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	return os.Rename(tmp, dst)
}
