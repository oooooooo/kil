//go:build !linux

package signal

import (
	"syscall"
	"testing"
)

// failDelivery makes the signal fail with err when it is delivered.
func failDelivery(t *testing.T, err error) {
	t.Helper()
	saved := kill
	t.Cleanup(func() { kill = saved })
	kill = func(int, syscall.Signal) error { return err }
}
