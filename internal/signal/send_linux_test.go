package signal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/oooooooo/kil/internal/proc"
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

// Running out of file descriptors or memory while signalling, or while
// probing pidfd, is not a system without pidfd either.
func TestSendDoesNotFallBackOnResourceErrorsAnywhere(t *testing.T) {
	t.Run("signal", func(t *testing.T) {
		requirePidfd(t)
		cmd := startSleep(t)
		failPidfdSignal(t, unix.ENOMEM, func(unix.Signal) bool { return true })
		got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
		if got != Failed || !errors.Is(err, unix.ENOMEM) {
			t.Errorf("Send() = %v, %v; want Failed with ENOMEM", got, err)
		}
		if !stillRunning(cmd) {
			t.Error("process was signalled")
		}
	})
	t.Run("open, with the probe denied", func(t *testing.T) {
		cmd := startSleep(t)
		target := cmd.Process.Pid
		failPidfdOpen(t, unix.ENOMEM, func(pid int) bool { return pid == target })
		failPidfdOpen(t, unix.EPERM, func(pid int) bool { return pid != target })
		got, err := Send(target, syscall.SIGTERM, "the line", fixed("the line", true), nil)
		if got != Failed || !errors.Is(err, unix.ENOMEM) {
			t.Errorf("Send() = %v, %v; want Failed with ENOMEM", got, err)
		}
		if !stillRunning(cmd) {
			t.Error("process was signalled")
		}
	})
}

// A probe that fails for lack of memory while signalling kil itself does
// not show that pidfd is unavailable, so a target's own failure is
// reported rather than its PID signalled.
func TestSendDoesNotFallBackWhenTheProbeSignalLacksResources(t *testing.T) {
	requirePidfd(t)
	cmd := startSleep(t)
	failPidfdSignal(t, unix.EPERM, func(sig unix.Signal) bool { return sig != 0 })
	failPidfdSignal(t, unix.ENOMEM, func(sig unix.Signal) bool { return sig == 0 })
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Failed || !errors.Is(err, unix.EPERM) {
		t.Errorf("Send() = %v, %v; want Failed with EPERM", got, err)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

// With pidfd, SendAll checks every target in one lookup, after opening a
// pidfd for each, rather than running ps once per target.
func TestSendAllLooksTargetsUpTogether(t *testing.T) {
	requirePidfd(t)
	cmds, targets := targetsOf(t, 5)
	calls := 0
	var o outcomes
	SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), nil, o.done)
	if calls != 1 || !slices.Equal(o.got, []Outcome{Sent, Sent, Sent, Sent, Sent}) {
		t.Errorf("lookups = %d, outcomes = %v; want 1 and all sent", calls, o.got)
	}
	for _, cmd := range cmds {
		if !exited(cmd) {
			t.Error("a process did not exit")
		}
	}
}

// Without pidfd, each target is checked right before its signal, so that
// a PID reused while others are signalled is caught.
func TestSendAllChecksEachTargetWithoutPidfd(t *testing.T) {
	cmds, targets := targetsOf(t, 3)
	failPidfdOpen(t, unix.EPERM, func(int) bool { return true })
	calls := 0
	var o outcomes
	SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), nil, o.done)
	if calls != 3 || !slices.Equal(o.got, []Outcome{Sent, Sent, Sent}) {
		t.Errorf("lookups = %d, outcomes = %v; want 3 and all sent", calls, o.got)
	}
	for _, cmd := range cmds {
		if !exited(cmd) {
			t.Error("a process did not exit")
		}
	}
}

// startExecer starts a shell that replaces itself with another command
// when a line reaches its input, and returns a function that makes it do
// so and waits until it has.
func startExecer(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "read line; exec sleep 123")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd, func() {
		_, _ = in.Write([]byte("go\n"))
		cmdline := fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if b, err := os.ReadFile(cmdline); err == nil && strings.HasPrefix(string(b), "sleep\x00123") {
				return
			}
		}
		t.Fatal("the shell did not exec")
	}
}

// A target checked with the others but replaced by another command while
// earlier targets were signalled is not signalled: a pidfd keeps the
// process, but not its command, across exec. The change shows in /proc, and
// ps, asked again, no longer shows the line selected.
func TestSendAllSkipsATargetThatExecsAfterTheCheck(t *testing.T) {
	requirePidfd(t)
	first := startSleep(t)
	second, execNow := startExecer(t)
	pids := []int{first.Process.Pid, second.Process.Pid}
	selected, err := proc.CurrentAll(pids)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{PID: pids[0], Want: selected[pids[0]]}, {PID: pids[1], Want: selected[pids[1]]}}
	calls := 0
	lookup := func(pids []int) (map[int]string, error) {
		calls++
		return proc.CurrentAll(pids)
	}
	var o outcomes
	SendAll(targets, 0, lookup, nil, func(i int, out Outcome, err error) {
		o.done(i, out, err)
		if i == 0 {
			execNow()
		}
	})
	if calls != 2 || !slices.Equal(o.got, []Outcome{Sent, Skipped}) {
		t.Errorf("lookups = %d, outcomes = %v; want 2, the second for the changed one, and [sent skipped]", calls, o.got)
	}
}

// Once asked to stop, SendAll starts no lookup, which can take a while.
func TestSendAllDoesNotLookUpOnceStopped(t *testing.T) {
	_, targets := targetsOf(t, 2)
	calls := 0
	var o outcomes
	SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), func() bool { return true }, o.done)
	if calls != 0 || !slices.Equal(o.got, []Outcome{Cancelled}) {
		t.Errorf("lookups = %d, outcomes = %v; want none and [cancelled]", calls, o.got)
	}
}

// A command line or name a process rewrites itself, as with prctl, while
// ps still shows the line selected, is checked again on its own rather than
// skipped.
func TestSendAllRechecksAChangedCommandBeforeSkipping(t *testing.T) {
	requirePidfd(t)
	cmds, targets := targetsOf(t, 1)
	saved := readCommand
	t.Cleanup(func() { readCommand = saved })
	reads := 0
	readCommand = func(int) (string, bool) {
		reads++
		return fmt.Sprintf("read %d", reads), true
	}
	calls := 0
	var o outcomes
	SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), nil, o.done)
	if calls != 2 || !slices.Equal(o.got, []Outcome{Sent}) || !exited(cmds[0]) {
		t.Errorf("lookups = %d, outcomes = %v; want 2 and [sent]", calls, o.got)
	}
}

// SendAll holds no more pidfds at once than the open-file limit leaves room
// for, with some to spare for ps and /proc, as a batch that took them all
// left ps unable to start and failed every target.
func TestSendAllKeepsFileDescriptorsForPs(t *testing.T) {
	requirePidfd(t)
	saved := openFiles
	t.Cleanup(func() { openFiles = saved })
	openFiles = func() (limit, open int) { return 100, 10 }
	_, targets := targetsOf(t, 60)
	most := 0
	var o outcomes
	lookup := func(pids []int) (map[int]string, error) {
		most = max(most, len(pids))
		found := map[int]string{}
		for _, pid := range pids {
			for _, t := range targets {
				if t.PID == pid {
					found[pid] = t.Want
				}
			}
		}
		return found, nil
	}
	SendAll(targets, 0, lookup, nil, o.done)
	if most > 100-10-fdReserve || len(o.got) != 60 {
		t.Errorf("a lookup held %d pidfds with room for %d; %d outcomes, want 60", most, 100-10-fdReserve, len(o.got))
	}
}

// A stop that comes while /proc is read right before a signal keeps that
// signal from being sent, and starts no further check.
func TestSendAllStopsAfterTheLastCommandRead(t *testing.T) {
	requirePidfd(t)
	for name, changed := range map[string]bool{"unchanged": false, "changed": true} {
		t.Run(name, func(t *testing.T) {
			cmds, targets := targetsOf(t, 1)
			saved := readCommand
			t.Cleanup(func() { readCommand = saved })
			reads, stop := 0, false
			readCommand = func(pid int) (string, bool) {
				reads++
				if reads == 2 {
					stop = true
					if changed {
						return "changed", true
					}
				}
				return saved(pid)
			}
			calls := 0
			var o outcomes
			SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), func() bool { return stop }, o.done)
			if calls != 1 || !slices.Equal(o.got, []Outcome{Cancelled}) || !stillRunning(cmds[0]) {
				t.Errorf("lookups = %d, outcomes = %v; want 1, [cancelled], and the process not signalled", calls, o.got)
			}
		})
	}
}
