package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func openFDs(t *testing.T, pid int) int {
	t.Helper()
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatal(err)
	}
	return len(fds)
}

// suspendAndResume presses Ctrl-Z, waits for kil to stop, sends what is
// typed while it is stopped, and brings it back as fg does.
func (k *ptyKil) suspendAndResume(withCtrlZ, whileStopped string) {
	k.t.Helper()
	k.send("\x1a" + withCtrlZ)
	k.eventually("kil to stop", func() bool { return k.state() == "T" })
	if whileStopped != "" {
		k.send(whileStopped)
	}
	from := k.mark()
	if err := unix.Kill(k.pid, unix.SIGCONT); err != nil {
		k.t.Fatal(err)
	}
	k.waitFor(from, "Alt-H help")
}

// Suspending and resuming kil many times leaves no file descriptor behind.
func TestRepeatedSuspendKeepsFDs(t *testing.T) {
	k := startKil(t)
	k.suspendAndResume("", "")
	before := openFDs(t, k.pid)
	for range 20 {
		k.suspendAndResume("", "")
	}
	if after := openFDs(t, k.pid); after > before {
		t.Errorf("open file descriptors went from %d to %d over 20 suspends", before, after)
	}
	k.send("\x03")
	if code := k.code(); code != exitAborted {
		t.Errorf("Ctrl-C: exit status %d, want %d", code, exitAborted)
	}
}

// Keys typed with Ctrl-Z, while kil is stopped, and after fg reach the
// query in the order they were typed.
func TestKeysAroundSuspendKeepOrder(t *testing.T) {
	name := fmt.Sprintf("kilorder%d", time.Now().UnixNano())
	sleeper := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$", name)
	sleeper.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = sleeper.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		<-exited
	})

	k := startKil(t, "KIL_EXACT=1")
	k.suspendAndResume(name[:4], name[4:8])
	k.send(name[8:])
	time.Sleep(time.Second)
	from := k.mark()
	k.send("\r")
	if code := k.code(); code != exitOK {
		out, _ := os.ReadFile(filepath.Join(k.dir, "stdout"))
		t.Fatalf("exit status %d, want %d; kil reported:\n%s", code, exitOK, out)
	}
	out, _ := os.ReadFile(filepath.Join(k.dir, "stdout"))
	if !strings.Contains(string(out), name) {
		t.Errorf("kil signalled something other than %s:\n%s", name, out)
	}
	if !k.restored(from) {
		t.Errorf("terminal not restored; output %q", k.output(from))
	}
}

// Ctrl-Z returns the terminal at once, without waiting for the input
// reader, even when keys follow it.
func TestSuspendIsPrompt(t *testing.T) {
	k := startKil(t)
	start := time.Now()
	k.send("\x1aab")
	k.eventually("kil to stop", func() bool { return k.state() == "T" })
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("kil stopped %v after Ctrl-Z", d)
	}
	if err := unix.Kill(k.pid, unix.SIGCONT); err != nil {
		t.Fatal(err)
	}
	k.send("\x03")
	if code := k.code(); code != exitAborted {
		t.Errorf("Ctrl-C: exit status %d, want %d", code, exitAborted)
	}
}
