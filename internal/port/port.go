// Package port finds the processes that hold network ports: a listening
// TCP socket or a bound UDP one. A client connected to the port is not
// one, as killing a browser open on a dev server is not what is meant.
package port

// A Result is what Find found.
type Result struct {
	// PIDs are the processes holding the ports.
	PIDs map[int]bool
	// Hidden maps a port to the users of sockets on it whose processes kil
	// could not see, as another user's without root.
	Hidden map[int][]int
	// MayMiss says that other users' processes may be missing without a
	// trace, where the system does not show their sockets at all.
	MayMiss bool
}
