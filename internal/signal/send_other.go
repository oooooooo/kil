//go:build !linux

package signal

import "syscall"

func send(pid int, sig syscall.Signal, unchanged func() bool) Outcome {
	return sendWithKill(pid, sig, unchanged)
}
