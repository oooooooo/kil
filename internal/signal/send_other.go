//go:build !linux

package signal

import "syscall"

// A handle signals one process by its PID, checked right before the
// signal. The PID can still be reused in that moment; only Linux's pidfd
// closes it.
type handle struct {
	byPID bool
}

func open(int) (handle, error) { return handle{byPID: true}, nil }

func (handle) signal(pid int, sig syscall.Signal) error { return kill(pid, sig) }

// command is not known without /proc, so each process is checked on its
// own right before its signal.
func (handle) command(int) (string, bool) { return "", false }

func (handle) close() {}

// batchLimit returns how many processes a batch takes; without pidfd it
// holds no file for them.
func batchLimit() int { return batchSize }
