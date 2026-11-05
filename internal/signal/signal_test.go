package signal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want syscall.Signal
	}{
		{"", syscall.SIGKILL},
		{"9", syscall.SIGKILL},
		{"15", syscall.SIGTERM},
		{"0", syscall.Signal(0)},
		{"TERM", syscall.SIGTERM},
		{"SIGTERM", syscall.SIGTERM},
		{"term", syscall.SIGTERM},
		{"hup", syscall.SIGHUP},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("Parse(%q) = %v, %v; want %v, nil", tt.in, got, err, tt.want)
		}
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	for _, in := range []string{"-9", "-1", "999", "137", "NOPE", "9x", " 9", "SIG"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, nil; want an error", in, got)
		}
	}
}

func TestLabel(t *testing.T) {
	if got := Label(syscall.SIGKILL); got != "SIGKILL" {
		t.Errorf("Label(SIGKILL) = %q", got)
	}
	if got := Label(syscall.Signal(0)); got != "signal 0" {
		t.Errorf("Label(0) = %q", got)
	}
}

func startSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	// Start can return while the kernel is still setting up the new
	// command line, which SendAll reads to notice an exec, so where /proc
	// shows it, wait for it to be sleep's.
	cmdline := fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if b, err := os.ReadFile(cmdline); err != nil || strings.HasPrefix(string(b), "sleep\x00") {
			break
		}
	}
	return cmd
}

// stillRunning reports whether cmd keeps running for a moment, long enough
// for a signal sent to it to take effect. Signal 0 cannot tell, as it still
// reaches a child that has exited and is not yet reaped.
func stillRunning(cmd *exec.Cmd) bool {
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil)
		if err != nil || pid != 0 {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

func exited(cmd *exec.Cmd) bool {
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func fixed(line string, ok bool) func(int) (string, bool, error) {
	return func(int) (string, bool, error) { return line, ok, nil }
}

func TestSendSignalsWhenLineIsUnchanged(t *testing.T) {
	cmd := startSleep(t)
	got, _ := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Sent {
		t.Fatalf("Send() = %v, want Sent", got)
	}
	if !exited(cmd) {
		t.Error("process did not exit after SIGTERM")
	}
}

func TestSendSkipsWhenLineChanged(t *testing.T) {
	cmd := startSleep(t)
	got, _ := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("another line", true), nil)
	if got != Skipped {
		t.Errorf("Send() = %v, want Skipped", got)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

func TestSendSkipsWhenProcessIsGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	got, _ := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("", false), nil)
	if got != Skipped {
		t.Errorf("Send() = %v, want Skipped", got)
	}
}

func TestSendRefusesNonPositivePIDs(t *testing.T) {
	for _, pid := range []int{0, -1} {
		// Signal 0 so that a regression cannot hit a real process group.
		if got, _ := Send(pid, syscall.Signal(0), "x", fixed("x", true), nil); got != Skipped {
			t.Errorf("Send(%d) = %v, want Skipped", pid, got)
		}
	}
}

// kil stops signalling on SIGINT or SIGTERM. Checking the process can take a
// while, as it runs ps, so a stop that arrives meanwhile must still keep the
// signal from being sent.
func TestSendCancelsWhenStoppedDuringTheCheck(t *testing.T) {
	cmd := startSleep(t)
	stopped := false
	current := func(int) (string, bool, error) {
		stopped = true
		return "the line", true, nil
	}
	got, _ := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", current, func() bool { return stopped })
	if got != Cancelled {
		t.Errorf("Send() = %v, want Cancelled", got)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

// A check that fails, as when ps does, is not a process that exited: Send
// fails with the reason and signals nothing.
func TestSendFailsWhenTheCheckFails(t *testing.T) {
	cmd := startSleep(t)
	checkErr := errors.New("ps failed: exit status 2")
	current := func(int) (string, bool, error) { return "", false, checkErr }
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", current, nil)
	if got != Failed || !errors.Is(err, checkErr) {
		t.Errorf("Send() = %v, %v; want Failed with the check's error", got, err)
	}
	if !stillRunning(cmd) {
		t.Error("process was signalled")
	}
}

// When the signal itself fails, Send says why, such as a lack of permission.
func TestSendReportsWhySignallingFailed(t *testing.T) {
	cmd := startSleep(t)
	failDelivery(t, unix.EPERM)
	got, err := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Failed || !errors.Is(err, unix.EPERM) {
		t.Errorf("Send() = %v, %v; want Failed with EPERM", got, err)
	}
}

// The aliases signal.h defines, such as IOT for ABRT, name the same
// signal, which kil reports by its usual name.
func TestParseAcceptsAliases(t *testing.T) {
	tests := map[string]string{"IOT": "SIGABRT", "SIGIOT": "SIGABRT", "iot": "SIGABRT"}
	if runtime.GOOS == "linux" {
		tests["CLD"] = "SIGCHLD"
		tests["POLL"] = "SIGIO"
	}
	for in, want := range tests {
		got, err := Parse(in)
		if err != nil || Label(got) != want {
			t.Errorf("Parse(%q) = %v, %v; want %s", in, Label(got), err, want)
		}
	}
}

// targetsOf starts n sleeping children and returns them as targets whose
// identity is "line N".
func targetsOf(t *testing.T, n int) ([]*exec.Cmd, []Target) {
	t.Helper()
	var cmds []*exec.Cmd
	var targets []Target
	for i := range n {
		cmd := startSleep(t)
		cmds = append(cmds, cmd)
		targets = append(targets, Target{PID: cmd.Process.Pid, Want: fmt.Sprintf("line %d", i)})
	}
	return cmds, targets
}

// lookupOf returns a lookup that finds each target with its identity, and
// counts how many times it ran.
func lookupOf(targets []Target, calls *int) Lookup {
	return func(pids []int) (map[int]string, error) {
		*calls++
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
}

// outcomes collects what SendAll reports, in order.
type outcomes struct {
	got  []Outcome
	errs []error
}

func (o *outcomes) done(i int, out Outcome, err error) {
	o.got = append(o.got, out)
	o.errs = append(o.errs, err)
}

// SendAll reports one outcome per target, in order, and stops at a target
// it is asked to stop before, leaving the rest to the caller.
func TestSendAllStopsWhereAsked(t *testing.T) {
	cmds, targets := targetsOf(t, 3)
	calls := 0
	var o outcomes
	stopped := func() bool { return len(o.got) == 1 }
	SendAll(targets, syscall.SIGTERM, lookupOf(targets, &calls), stopped, o.done)
	if !slices.Equal(o.got, []Outcome{Sent, Cancelled}) {
		t.Fatalf("outcomes = %v, want [sent cancelled]", o.got)
	}
	if !exited(cmds[0]) || !stillRunning(cmds[1]) || !stillRunning(cmds[2]) {
		t.Error("want only the first process signalled")
	}
}

// A target whose identity changed is skipped, and a failed lookup fails
// the targets it was to check, with its reason.
func TestSendAllSkipsChangedTargetsAndFailsUncheckedOnes(t *testing.T) {
	cmds, targets := targetsOf(t, 2)
	targets[1].Want = "another line"
	calls := 0
	var o outcomes
	SendAll(targets, syscall.SIGTERM, lookupOf(targets[:1], &calls), nil, o.done)
	if !slices.Equal(o.got, []Outcome{Sent, Skipped}) || !exited(cmds[0]) || !stillRunning(cmds[1]) {
		t.Errorf("outcomes = %v; want [sent skipped] with only the first signalled", o.got)
	}

	cmds, targets = targetsOf(t, 2)
	lookupErr := errors.New("ps failed")
	o = outcomes{}
	SendAll(targets, syscall.SIGTERM, func([]int) (map[int]string, error) { return nil, lookupErr }, nil, o.done)
	if !slices.Equal(o.got, []Outcome{Failed, Failed}) || !errors.Is(o.errs[0], lookupErr) || !stillRunning(cmds[0]) || !stillRunning(cmds[1]) {
		t.Errorf("outcomes = %v, %v; want both failed with the lookup's error and none signalled", o.got, o.errs)
	}
}
