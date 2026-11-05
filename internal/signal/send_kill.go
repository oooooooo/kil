package signal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// sendWithKill checks the process and then signals its PID. The PID can
// still be reused between the two steps; only Linux's pidfd closes that.
func sendWithKill(pid int, sig syscall.Signal, unchanged func() (bool, error)) (Outcome, error) {
	if ok, err := unchanged(); !ok {
		return unchecked(err)
	}
	return outcomeOf(kill(pid, sig))
}

// kill is kill(2). Tests replace it to make a signal fail.
var kill = unix.Kill
