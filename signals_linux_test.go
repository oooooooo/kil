package main

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/oooooooo/kil/internal/picker"
)

// signalAll checks all its targets with one lookup where pidfd works,
// rather than running ps once per target: 200 targets took seconds.
func TestSignalAllLooksTargetsUpTogether(t *testing.T) {
	if fd, err := unix.PidfdOpen(os.Getpid(), 0); err != nil {
		t.Skipf("pidfd does not work here: %v", err)
	} else {
		_ = unix.Close(fd)
	}
	targets := []picker.Item{ownSleeper(t), ownSleeper(t), ownSleeper(t)}
	saved := lookupLines
	t.Cleanup(func() { lookupLines = saved })
	calls := 0
	lookupLines = func(pids []int) (map[int]string, error) {
		calls++
		return saved(pids)
	}
	var stdout, stderr bytes.Buffer
	if code := signalAll(targets, 0, &stdout, &stderr, nil); code != exitOK || calls != 1 {
		t.Errorf("signalAll = %d after %d lookups, stderr %q; want %d after 1", code, calls, stderr.String(), exitOK)
	}
}
