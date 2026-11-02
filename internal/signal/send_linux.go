package signal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// send opens a pidfd before checking the process, so the signal goes to the
// process that was checked even if it exits and its PID is reused between
// the check and the signal. Kernels older than 5.3 have no pidfd and fall
// back to kill(2), which leaves that window open.
func send(pid int, sig syscall.Signal, unchanged func() bool) Outcome {
	fd, err := unix.PidfdOpen(pid, 0)
	if err == unix.ENOSYS {
		return sendWithKill(pid, sig, unchanged)
	}
	if err != nil {
		return outcomeOf(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if !unchanged() {
		return Skipped
	}
	return outcomeOf(unix.PidfdSendSignal(fd, sig, nil, 0))
}
