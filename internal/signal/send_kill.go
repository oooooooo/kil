package signal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// sendWithKill checks the process and then signals its PID. The PID can
// still be reused between the two steps; only Linux's pidfd closes that.
func sendWithKill(pid int, sig syscall.Signal, unchanged func() bool) Outcome {
	if !unchanged() {
		return Skipped
	}
	return outcomeOf(unix.Kill(pid, sig))
}
