package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/ui"
)

// ownSleeper starts a process for kil to signal and returns it as a picker
// row, as kil lists it.
func ownSleeper(t *testing.T) picker.Item {
	t.Helper()
	cmd := exec.Command("sleep", "1000")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	procs, err := proc.List(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs {
		if p.PID == cmd.Process.Pid {
			return picker.Item{ID: p.Identity, Text: p.Raw}
		}
	}
	t.Fatalf("process %d not listed", cmd.Process.Pid)
	return picker.Item{}
}

func caught(sigs ...syscall.Signal) *interrupts {
	in := &interrupts{}
	for _, s := range sigs {
		in.catch(s)
	}
	return in
}

// The first signal caught ends the picker, and the first SIGINT, SIGTERM, or
// SIGQUIT also stops signalling and sets the exit status, even after a
// SIGHUP.
func TestInterruptsRecordTheFirstSignalAndTheFirstStop(t *testing.T) {
	in := caught(syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	if in.first() != syscall.SIGHUP || in.stopping() != syscall.SIGTERM {
		t.Errorf("first() = %v, stopping() = %v; want hangup and terminated", in.first(), in.stopping())
	}
	if code, ok := in.ended(); !ok || code != 143 {
		t.Errorf("ended() = %d, %v; want 143, true", code, ok)
	}
	if code, ok := caught(syscall.SIGHUP).ended(); !ok || code != 129 {
		t.Errorf("SIGHUP alone: ended() = %d, %v; want 129, true", code, ok)
	}
	var none *interrupts
	if _, ok := none.ended(); ok || none.first() != nil || none.stopping() != nil {
		t.Error("a nil *interrupts reports a signal")
	}
}

// When KIL_TEST_HANGUPS is set, this test is the child process of
// TestHangupsDoNotHideATermination: it sends itself many SIGHUPs and then
// SIGTERM, and exits 0 once kil's handler has recorded the SIGTERM.
func TestHangupsChild(t *testing.T) {
	if os.Getenv("KIL_TEST_HANGUPS") == "" {
		t.Skip("run by TestHangupsDoNotHideATermination")
	}
	in := watchSignals()
	// Holding the lock keeps kil's handler from taking signals off its
	// channels, as when it is busy, so they queue as they would then.
	in.mu.Lock()
	// Signals sent at once are merged into one, so they are spaced.
	for range 5 {
		_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	in.mu.Unlock()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if in.stopping() == syscall.SIGTERM {
			os.Exit(0)
		}
	}
	os.Exit(3)
}

// Signals reach kil through channels that drop what does not fit, so
// SIGHUPs, which kil ignores while signalling, must not crowd out SIGTERM.
func TestHangupsDoNotHideATermination(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHangupsChild$")
	cmd.Env = append(os.Environ(), "KIL_TEST_HANGUPS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("SIGTERM after many SIGHUPs was not recorded: %v\n%s", err, out)
	}
}

// SIGINT, SIGTERM, or SIGQUIT while kil is signalling stops it, as with
// kill(1), and the processes not reached are reported. SIGHUP does not: it
// comes when the terminal goes away, as when kil has just killed the shell
// it was started from, and the rest of the selection must still be
// signalled.
func TestSignalsWhileSignalling(t *testing.T) {
	tests := []struct {
		got     syscall.Signal
		code    int
		sent    int
		skipped int
	}{
		{syscall.SIGINT, 130, 0, 2},
		{syscall.SIGTERM, 143, 0, 2},
		{syscall.SIGQUIT, 131, 0, 2},
		{syscall.SIGHUP, exitOK, 2, 0},
	}
	for _, tt := range tests {
		targets := []picker.Item{ownSleeper(t), ownSleeper(t)}
		var stdout, stderr bytes.Buffer
		// Signal 0 only checks that the process exists.
		code := signalAll(targets, 0, &stdout, &stderr, caught(tt.got))
		sent := strings.Count(stdout.String(), "sent ")
		skipped := strings.Count(stderr.String(), "skipped: not signalled: ")
		if code != tt.code || sent != tt.sent || skipped != tt.skipped {
			t.Errorf("%v while signalling: code %d, %d sent, %d skipped; want %d, %d, %d\nstdout:\n%s\nstderr:\n%s",
				tt.got, code, sent, skipped, tt.code, tt.sent, tt.skipped, stdout.String(), stderr.String())
		}
	}
}

// Checking a process runs ps, which takes a while. SIGTERM arriving then
// must keep that process from being signalled and must not be lost, even
// for the last process.
func TestStopDuringTheLastCheck(t *testing.T) {
	target := ownSleeper(t)
	in := &interrupts{}
	saved := currentLine
	t.Cleanup(func() { currentLine = saved })
	currentLine = func(pid int) (string, bool) {
		in.catch(syscall.SIGTERM)
		return saved(pid)
	}
	var stdout, stderr bytes.Buffer
	code := signalAll([]picker.Item{target}, 0, &stdout, &stderr, in)
	if code != 143 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "skipped: not signalled: ") {
		t.Errorf("SIGTERM during the last check: code %d, stdout %q, stderr %q; want 143 and the process skipped", code, stdout.String(), stderr.String())
	}
}

// A SIGTERM that comes as the last process is handled ends kil with its
// status, rather than the status of the processes handled.
func TestStopAfterTheLastProcess(t *testing.T) {
	target := ownSleeper(t)
	in := &interrupts{}
	saved := currentLine
	t.Cleanup(func() { currentLine = saved })
	// The process looks changed, so it is skipped rather than cancelled.
	currentLine = func(int) (string, bool) {
		in.catch(syscall.SIGTERM)
		return "another line", true
	}
	var stdout, stderr bytes.Buffer
	if code := signalAll([]picker.Item{target}, 0, &stdout, &stderr, in); code != 143 {
		t.Errorf("SIGTERM as the last process was skipped: code %d, want 143", code)
	}
}

// A partial Enter signals inside the picker, and SIGTERM must stop it there
// as well.
func TestPartialEnterStopsOnTermination(t *testing.T) {
	var b batches
	send := sender(&b, 0, caught(syscall.SIGTERM))
	send([]picker.Item{ownSleeper(t), ownSleeper(t)})
	var stdout, stderr bytes.Buffer
	b.flush(&stdout, &stderr)
	if stdout.Len() != 0 || strings.Count(stderr.String(), "skipped: not signalled: ") != 2 {
		t.Errorf("partial Enter after SIGTERM: stdout %q, stderr %q; want both skipped", stdout.String(), stderr.String())
	}
}

// SIGHUP does not stop a partial Enter already signalling, but one caught
// while picking ends kil before the next batch starts: the picker would
// have ended on it, had the Enter not come first.
func TestPartialEnterAfterAHangupSignalsNothing(t *testing.T) {
	var b batches
	it := ownSleeper(t)
	sender(&b, 0, caught(syscall.SIGHUP))([]picker.Item{it})
	var stdout, stderr bytes.Buffer
	b.flush(&stdout, &stderr)
	if stdout.Len() != 0 || stderr.String() != "skipped: not signalled: "+it.Text+"\n" || b.sent != 0 {
		t.Errorf("partial Enter after SIGHUP: stdout %q, stderr %q, sent %d; want it skipped", stdout.String(), stderr.String(), b.sent)
	}
}

// When the terminal fails as a signal ends the picker, as when it hangs up,
// kil still ends with the signal's status, after reporting the failure.
func TestPickerFailureAfterASignalEndsWithItsStatus(t *testing.T) {
	var b batches
	var stdout, stderr bytes.Buffer
	code := pickFailed(&b, errors.New("picker failed: read /dev/tty: input/output error"), &stdout, &stderr, caught(syscall.SIGHUP))
	if code != 129 || !strings.Contains(stderr.String(), "input/output error") {
		t.Errorf("pickFailed after SIGHUP = %d, stderr %q; want 129 and the error", code, stderr.String())
	}
}

// A signal that ends the picker ends kil with 128 plus its number, as a
// shell reports a command killed by it, after the reports of a partial
// Enter. SIGTERM, SIGINT, or SIGQUIT wins over an earlier SIGHUP.
func TestSignalWhilePickingEndsWithItsStatus(t *testing.T) {
	tests := []struct {
		sigs []syscall.Signal
		want int
	}{
		{[]syscall.Signal{syscall.SIGHUP}, 129},
		{[]syscall.Signal{syscall.SIGINT}, 130},
		{[]syscall.Signal{syscall.SIGQUIT}, 131},
		{[]syscall.Signal{syscall.SIGTERM}, 143},
		{[]syscall.Signal{syscall.SIGHUP, syscall.SIGTERM}, 143},
	}
	for _, tt := range tests {
		var b batches
		reportln(b.stdout(), "sent SIGKILL: 101 a")
		var stdout, stderr bytes.Buffer
		code := finish(ui.New(nil, "", "SIGKILL", nil), syscall.SIGKILL, &b, &stdout, &stderr, caught(tt.sigs...))
		if code != tt.want || stdout.String() != "sent SIGKILL: 101 a\n" {
			t.Errorf("%v while picking: code %d, stdout %q; want %d and the earlier report", tt.sigs, code, stdout.String(), tt.want)
		}
	}
}

// Without a shell to bring kil back, as when a terminal emulator or tmux
// runs kil directly, stopping kil would leave it stopped for good, so
// Ctrl-Z does nothing. Nor does it once kil is ending.
func TestSuspendIsIgnored(t *testing.T) {
	running := ui.New([]picker.Item{{ID: "101 a", Text: "101 a"}}, "", "SIGKILL", nil)
	aborted, _ := running.Update(ui.KeyPressMsg{Code: 'c', Mod: ui.ModCtrl})
	for name, tt := range map[string]struct {
		m          ui.Model
		in         *interrupts
		jobControl bool
		want       bool
	}{
		"while picking":       {running, nil, true, true},
		"without job control": {running, nil, false, false},
		"after Ctrl-C":        {aborted, nil, true, false},
		"after a signal":      {running, caught(syscall.SIGTERM), true, false},
	} {
		if got := canSuspend(tt.in, func() bool { return tt.jobControl })(tt.m); got != tt.want {
			t.Errorf("%s: canSuspend = %v, want %v", name, got, tt.want)
		}
	}
}
