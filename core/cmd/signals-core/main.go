// signals-core: Signals' cross-platform engine (macOS, Linux, Windows).
//
//	signals-core hook <agent> [event]   record a hook event (event JSON on stdin)
//	signals-core state                  every session, as JSON
//	signals-core version
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/samhudson/signals/core/engine"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "hook":
		if len(os.Args) < 3 {
			os.Exit(0)
		}
		event := ""
		if len(os.Args) > 3 {
			event = os.Args[3]
		}
		input, _ := io.ReadAll(os.Stdin)
		os.Exit(engine.Hook{Agent: os.Args[2], Event: event, Input: input, Now: time.Now(),
			Stdout: os.Stdout, Stderr: os.Stderr, Gate: gate()}.Run())
	case "state":
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(engine.Sessions(time.Now())); err != nil {
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println(version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: signals-core hook <agent> [event] | state | version")
	os.Exit(1)
}

// gate runs the Usage budget gate next to this program, when there is one.
func gate() func([]byte) (int, string) {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	path := filepath.Join(filepath.Dir(exe), "signals-gate")
	if fi, err := os.Stat(path); err != nil || fi.Mode()&0o111 == 0 {
		return nil
	}
	return func(input []byte) (int, string) {
		cmd := exec.Command(path)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), stderr.String()
		}
		return 0, ""
	}
}
