// Package client is the daemon's client, as AutoDoc's own programs use it: a Session (the
// handshake, the connection's generation, spawning --serve when nothing answers), the restart
// handoffs that decide which build serves next, and the waits around a restart. The TUI, --call,
// --print-endpoint and --restart all connect through it.
//
// A Session is safe for concurrent use: its state is under one mutex, and a call runs on whatever
// goroutine makes it.
package client
