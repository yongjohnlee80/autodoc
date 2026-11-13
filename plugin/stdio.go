package plugin

import (
	"errors"
	"os"
)

// keptStdout is the process's stdout as it started. stdio points os.Stdout at os.Stderr; without
// this reference the original *os.File would be garbage, and its finalizer would close the
// descriptor at the next GC: the pipe the host reads frames from, which ended every plugin some
// seconds in.
var keptStdout *os.File

// keepStdout keeps the process's stdout for the link and points os.Stdout at os.Stderr: stdout is
// the protocol's, and a stray print would break it.
func keepStdout() {
	keptStdout = os.Stdout
	os.Stdout = os.Stderr
}

// refuseTerminal refuses a stdin that is a terminal: a plugin runs under AutoDoc.
func refuseTerminal(stdin *os.File) error {
	if fi, err := stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return errors.New("plugin: stdin is a terminal; a plugin runs under AutoDoc, from its Plugins menu")
	}
	return nil
}
