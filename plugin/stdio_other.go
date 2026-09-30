//go:build !unix

package plugin

import (
	"errors"
	"net"
)

// stdio: plugins need a Unix system, where a pipe takes a write deadline.
func stdio() (net.Conn, error) { return nil, errors.New("plugin: plugins need Linux or macOS") }
