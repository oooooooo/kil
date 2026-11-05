//go:build !linux

package signal

import "syscall"

func send(pid int, sig syscall.Signal, unchanged func() (bool, error)) (Outcome, error) {
	return sendWithKill(pid, sig, unchanged)
}
