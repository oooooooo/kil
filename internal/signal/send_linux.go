package signal

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// send opens a pidfd before checking the process, so the signal goes to the
// process that was checked even if it exits and its PID is reused between
// the check and the signal. Without pidfd, as on kernels older than 5.3 or
// where seccomp denies pidfd_open or pidfd_send_signal, as Docker's older
// default profile does, it falls back to kill(2), which leaves that window
// open, as on macOS.
func send(pid int, sig syscall.Signal, unchanged func() (bool, error)) (Outcome, error) {
	fd, err := pidfdOpen(pid, 0)
	if err != nil && err != unix.ESRCH && pidfdUnavailable() {
		return sendWithKill(pid, sig, unchanged)
	}
	if err != nil {
		return outcomeOf(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if ok, err := unchanged(); !ok {
		return unchecked(err)
	}
	err = pidfdSendSignal(fd, sig, nil, 0)
	if err != nil && err != unix.ESRCH && pidfdUnavailable() {
		// The process was checked through the pidfd, which kil can open
		// but not signal through, so its PID is signalled, as on a system
		// without pidfd.
		return outcomeOf(kill(pid, sig))
	}
	return outcomeOf(err)
}

// pidfdSendSignal is pidfd_send_signal(2). Tests replace it to make a signal
// fail.
var pidfdSendSignal = unix.PidfdSendSignal

// pidfdOpen is pidfd_open(2). Tests replace it to make it fail.
var pidfdOpen = unix.PidfdOpen

// pidfdUnavailable reports whether kil cannot open a pidfd for itself, or
// signal itself through it with signal 0, which tells a system without
// pidfd, or one that denies it, from a failure that belongs to one target.
// Go's os package probes pidfd the same way. Running out of file
// descriptors or memory is not a lack of pidfd, as Rust's and Python's
// standard libraries also hold, so kil then reports the failure rather than
// give up the pidfd's protection.
func pidfdUnavailable() bool {
	fd, err := pidfdOpen(unix.Getpid(), 0)
	if err != nil {
		return !outOfResources(err)
	}
	defer func() { _ = unix.Close(fd) }()
	return pidfdSendSignal(fd, 0, nil, 0) != nil
}

func outOfResources(err error) bool {
	return err == unix.EMFILE || err == unix.ENFILE || err == unix.ENOMEM
}
