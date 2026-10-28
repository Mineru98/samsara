//go:build !linux && !darwin

package ipc

import "net"

// Other platforms rely on the 0700 runtime directory and 0600 socket mode.
func peerUID(c *net.UnixConn) (int, bool) { return 0, false }
