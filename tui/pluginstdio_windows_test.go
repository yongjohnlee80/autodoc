//go:build windows

package tui

import "os"

// testStdioFiles are the test plugin's stdin and stdout as plugin.Serve makes them on Windows
// (plugin/stdio_other.go): the process's own handles. os.NewFile takes a HANDLE there, not a
// descriptor, so 0 and 1 would name neither.
func testStdioFiles() (in, out *os.File) { return os.Stdin, os.Stdout }
