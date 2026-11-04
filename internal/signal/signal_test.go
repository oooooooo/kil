package signal

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
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
	return cmd
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

func fixed(line string, ok bool) func(int) (string, bool) {
	return func(int) (string, bool) { return line, ok }
}

func TestSendSignalsWhenLineIsUnchanged(t *testing.T) {
	cmd := startSleep(t)
	got := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("the line", true), nil)
	if got != Sent {
		t.Fatalf("Send() = %v, want Sent", got)
	}
	if !exited(cmd) {
		t.Error("process did not exit after SIGTERM")
	}
}

func TestSendSkipsWhenLineChanged(t *testing.T) {
	cmd := startSleep(t)
	got := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("another line", true), nil)
	if got != Skipped {
		t.Errorf("Send() = %v, want Skipped", got)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("process was signalled: %v", err)
	}
}

func TestSendSkipsWhenProcessIsGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	got := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", fixed("", false), nil)
	if got != Skipped {
		t.Errorf("Send() = %v, want Skipped", got)
	}
}

func TestSendRefusesNonPositivePIDs(t *testing.T) {
	for _, pid := range []int{0, -1} {
		// Signal 0 so that a regression cannot hit a real process group.
		if got := Send(pid, syscall.Signal(0), "x", fixed("x", true), nil); got != Skipped {
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
	current := func(int) (string, bool) {
		stopped = true
		return "the line", true
	}
	got := Send(cmd.Process.Pid, syscall.SIGTERM, "the line", current, func() bool { return stopped })
	if got != Cancelled {
		t.Errorf("Send() = %v, want Cancelled", got)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("process was signalled: %v", err)
	}
}
