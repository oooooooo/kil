package signal

import (
	"errors"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// failDelivery makes the signal fail with err however it is delivered.
func failDelivery(t *testing.T, err error) {
	t.Helper()
	failPidfdSignal(t, err, func(unix.Signal) bool { return true })
	saved := kill
	t.Cleanup(func() { kill = saved })
	kill = func(int, syscall.Signal) error { return err }
}

// failPidfdSignal makes pidfd_send_signal fail with err for the signals
// fails selects. kil probes pidfd with signal 0.
func failPidfdSignal(t *testing.T, err error, fails func(unix.Signal) bool) {
	t.Helper()
	saved := pidfdSendSignal
	t.Cleanup(func() { pidfdSendSignal = saved })
	pidfdSendSignal = func(fd int, sig unix.Signal, info *unix.Siginfo, flags int) error {
		if fails(sig) {
			return err
		}
		return saved(fd, sig, info, flags)
	}
}

// failPidfdOpen makes pidfd_open fail with err for the PIDs fails selects.
func failPidfdOpen(t *testing.T, err error, fails func(pid int) bool) {
	t.Helper()
	saved := pidfdOpen
	t.Cleanup(func() { pidfdOpen = saved })
	pidfdOpen = func(pid int, flags int) (int, error) {
		if fails(pid) {
			return -1, err
		}
		return saved(pid, flags)
	}
}

// Where seccomp denies pidfd_open, as Docker's older default profile does,
// kil checks the process and then signals its PID, as on kernels without
// pidfd and on macOS, rather than signal nothing.
func TestSendFallsBackToKillWhenPidfdIsDenied(t *testing.T) {
	cmd := startSleep(t)
	failPidfdOpen(t, unix.EPERM, func(int) bool { return true })
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Sent || err != nil {
		t.Fatalf("Send() = %v, %v; want Sent", got, err)
	}
	if !exited(cmd) {
		t.Error("process did not exit after SIGTERM")
	}
}

// When pidfd works for kil itself, a pidfd_open failure belongs to the
// target, so kil reports it rather than give up the pidfd's protection.
func TestSendFailsWhenOnlyTheTargetsPidfdFails(t *testing.T) {
	requirePidfd(t)
	cmd := startSleep(t)
	failPidfdOpen(t, unix.EMFILE, func(pid int) bool { return pid == cmd.Process.Pid })
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Failed || !errors.Is(err, unix.EMFILE) {
		t.Errorf("Send() = %v, %v; want Failed with EMFILE", got, err)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

// Where seccomp allows pidfd_open but denies pidfd_send_signal, kil
// signals the PID it has checked, rather than report a lack of permission
// that the user does not have.
func TestSendFallsBackToKillWhenPidfdSignalIsDenied(t *testing.T) {
	cmd := startSleep(t)
	failPidfdSignal(t, unix.EPERM, func(unix.Signal) bool { return true })
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Sent || err != nil {
		t.Fatalf("Send() = %v, %v; want Sent", got, err)
	}
	if !exited(cmd) {
		t.Error("process did not exit after SIGTERM")
	}
}

// When pidfd_send_signal works for kil itself, its failure for the target
// is the target's, such as a lack of permission, and is reported.
func TestSendFailsWhenOnlyTheTargetsSignalFails(t *testing.T) {
	requirePidfd(t)
	cmd := startSleep(t)
	failPidfdSignal(t, unix.EPERM, func(sig unix.Signal) bool { return sig != 0 })
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Failed || !errors.Is(err, unix.EPERM) {
		t.Errorf("Send() = %v, %v; want Failed with EPERM", got, err)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

// requirePidfd skips a test that needs pidfd to work for kil itself, as it
// does not on kernels older than 5.3, where kil falls back to kill(2).
func requirePidfd(t *testing.T) {
	t.Helper()
	fd, err := unix.PidfdOpen(unix.Getpid(), 0)
	if err != nil {
		t.Skipf("pidfd does not work here: %v", err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		t.Skipf("pidfd does not work here: %v", err)
	}
}

// Running out of file descriptors is not a system without pidfd: kil
// reports it rather than give up the pidfd's protection against a reused
// PID, as Rust's and Python's standard libraries also tell it apart.
func TestSendFailsWhenOutOfFileDescriptors(t *testing.T) {
	for _, errno := range []unix.Errno{unix.EMFILE, unix.ENFILE, unix.ENOMEM} {
		t.Run(errno.Error(), func(t *testing.T) {
			cmd := startSleep(t)
			failPidfdOpen(t, errno, func(int) bool { return true })
			got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
			if got != Failed || !errors.Is(err, errno) {
				t.Errorf("Send() = %v, %v; want Failed with %v", got, err, errno)
			}
			if !stillRunning(cmd) {
				t.Error("process was signalled")
			}
		})
	}
}
