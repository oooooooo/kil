package main

import (
	"fmt"
	"os"
	"os/exec"
	ossignal "os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// When KIL_TEST_RECORDER is set, this test is a process for kil to signal
// that appends each SIGTERM, SIGHUP, and SIGCONT it gets, with the time, to
// the file KIL_TEST_RECORDER names, and does not end on them, as a stubborn
// server. With KIL_TEST_EXIT_AFTER, it ends that long after SIGTERM, as a
// server shutting down. With KIL_TEST_EXEC_ON_TERM, it replaces itself with
// sleep on SIGTERM, so its command line changes. It writes "ready" first.
func TestRecorderChild(t *testing.T) {
	log := os.Getenv("KIL_TEST_RECORDER")
	if log == "" {
		t.Skip("run by the --timeout tests")
	}
	f, err := os.OpenFile(log, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		panic(err)
	}
	sigs := make(chan os.Signal, 8)
	ossignal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGCONT)
	_, _ = fmt.Fprintln(f, "ready")
	exitAfter, _ := time.ParseDuration(os.Getenv("KIL_TEST_EXIT_AFTER"))
	for s := range sigs {
		name := unix.SignalName(s.(syscall.Signal))
		_, _ = fmt.Fprintf(f, "%d %s\n", time.Now().UnixNano(), name)
		if s != syscall.SIGTERM {
			continue
		}
		if os.Getenv("KIL_TEST_EXEC_ON_TERM") != "" {
			_ = syscall.Exec("/bin/sleep", []string{"sleep", "60"}, os.Environ())
		}
		if exitAfter > 0 {
			time.Sleep(exitAfter)
			os.Exit(0)
		}
	}
}

// When KIL_TEST_ZOMBIE_PARENT is set, this test starts a child named by
// KIL_TEST_ZOMBIE_TAG, writes its PID to KIL_TEST_ZOMBIE_PIDFILE, and never
// waits for it, so the child stays a zombie once it ends. The child is a
// sleeper, or a recorder logging to KIL_TEST_ZOMBIE_LOG if that is set.
// The parent itself ends on SIGTERM.
func TestZombieParentChild(t *testing.T) {
	if os.Getenv("KIL_TEST_ZOMBIE_PARENT") == "" {
		t.Skip("run by the --timeout tests")
	}
	child := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$", os.Getenv("KIL_TEST_ZOMBIE_TAG"))
	child.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1", "KIL_TEST_ZOMBIE_PARENT=")
	if log := os.Getenv("KIL_TEST_ZOMBIE_LOG"); log != "" {
		child = exec.Command(os.Args[0], "-test.run=^TestRecorderChild$", os.Getenv("KIL_TEST_ZOMBIE_TAG"))
		child.Env = append(os.Environ(), "KIL_TEST_RECORDER="+log, "KIL_TEST_ZOMBIE_PARENT=")
	}
	if err := child.Start(); err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Getenv("KIL_TEST_ZOMBIE_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		panic(err)
	}
	time.Sleep(time.Minute)
}

// target is a process started for kil to signal.
type target struct {
	cmd    *exec.Cmd
	exited chan struct{}
	log    string
}

func (p *target) pid() int { return p.cmd.Process.Pid }

// startRecorder starts a recorder named by tag, with env added, and waits
// until it is ready to record signals.
func startRecorder(t *testing.T, tag string, env ...string) *target {
	t.Helper()
	log := filepath.Join(t.TempDir(), "signals")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecorderChild$", tag)
	cmd.Env = append(append(os.Environ(), "KIL_TEST_RECORDER="+log), env...)
	// The kernel drops SIGTSTP, SIGTTIN, and SIGTTOU sent to a process in
	// an orphaned process group, as the test's own may be; a group of its
	// own, whose parent is in another group of the session, is not one.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &target{cmd: cmd, exited: make(chan struct{}), log: log}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.exited
	})
	waitUntil(t, "the recorder to be ready", func() bool { return strings.HasPrefix(p.signals(t), "ready") })
	return p
}

// startPlain starts a sleeper, which ends on SIGTERM.
func startPlain(t *testing.T, tag string) *target {
	t.Helper()
	cmd, exited := startSleeper(t, tag)
	waitUntil(t, "the sleeper to start", func() bool {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
		return err == nil && strings.Contains(string(b), tag)
	})
	return &target{cmd: cmd, exited: exited}
}

// signals returns what the recorder logged.
func (p *target) signals(t *testing.T) string {
	b, _ := os.ReadFile(p.log)
	return string(b)
}

// got returns the times the recorder got sig.
func (p *target) got(t *testing.T, sig string) []time.Time {
	var times []time.Time
	for line := range strings.Lines(p.signals(t)) {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == sig {
			n, _ := strconv.ParseInt(f[0], 10, 64)
			times = append(times, time.Unix(0, n))
		}
	}
	return times
}

func (p *target) alive() bool {
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

func (p *target) endedBy() syscall.Signal {
	ws, _ := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ws.Signaled() {
		return -1
	}
	return ws.Signal()
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (k *ptyKil) waitExit(t *testing.T, p *target, within time.Duration) time.Time {
	t.Helper()
	select {
	case <-p.exited:
		return time.Now()
	case <-time.After(within):
		t.Fatalf("PID %d did not end within %v; kil reported:\n%s%s", p.pid(), within, k.stdout(), k.stderr())
	}
	return time.Time{}
}

func (k *ptyKil) stdout() string {
	b, _ := os.ReadFile(filepath.Join(k.dir, "stdout"))
	return string(b)
}

func (k *ptyKil) stderr() string {
	b, _ := os.ReadFile(filepath.Join(k.dir, "stderr"))
	return string(b)
}

// ended reports whether kil has exited.
func (k *ptyKil) ended() bool {
	_, err := os.Stat(filepath.Join(k.dir, "code"))
	return err == nil
}

// lines returns the report lines for pid, in order.
func lines(out string, pid int) []string {
	re := regexp.MustCompile(fmt.Sprintf(`^(.*?): +%d `, pid))
	var got []string
	for line := range strings.Lines(out) {
		if m := re.FindStringSubmatch(line); m != nil {
			got = append(got, m[1])
		}
	}
	return got
}

func checkReports(t *testing.T, what, out string, pid int, want ...string) {
	t.Helper()
	got := lines(out, pid)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s for PID %d = %q, want %q; all of it:\n%s", what, pid, got, want, out)
	}
}

// startKilWith starts kil with args and KIL_SIGNAL unset, its stdout and
// stderr apart.
func startKilWith(t *testing.T, env string, args ...string) *ptyKil {
	t.Helper()
	return startKil(t, env, "KIL_EXACT=1", "KIL_TEST_SPLIT=1", "KIL_TEST_ARGS="+strings.Join(args, "\n"))
}

// enter presses Enter once the header shows, after the rows have been on
// screen long enough for kil to take Enter.
func (k *ptyKil) enter(header string) {
	k.t.Helper()
	k.waitFor(0, header)
	time.Sleep(500 * time.Millisecond)
	k.send("\r")
}

// With --timeout, kil sends the signal, then SIGCONT, reports processes
// that end as they end, and sends SIGKILL to those still there when the
// timeout has passed since the picker ended.
func TestTimeoutKillsWhatIsLeft(t *testing.T) {
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	stubborn := startRecorder(t, tag)
	slow := startRecorder(t, tag, "KIL_TEST_EXIT_AFTER=300ms")
	quick := startPlain(t, tag)
	k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1.5", tag)
	k.waitFor(0, "Enter SIGTERM (KILL after 1.5s)")
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	k.enter("Enter SIGTERM (KILL after 1.5s)")

	k.waitExit(t, quick, 5*time.Second)
	k.waitExit(t, slow, 5*time.Second)
	// Each is reported as it ends, while kil still waits for the stubborn
	// one.
	waitUntil(t, "the exited processes to be reported", func() bool {
		return len(lines(k.stdout(), quick.pid())) == 2 && len(lines(k.stdout(), slow.pid())) == 2
	})
	if k.ended() || !stubborn.alive() {
		t.Errorf("kil ended or signalled the stubborn process before the timeout; it reported:\n%s", k.stdout())
	}

	killed := k.waitExit(t, stubborn, 5*time.Second)
	if got := stubborn.endedBy(); got != syscall.SIGKILL {
		t.Errorf("the stubborn process ended with %v, want SIGKILL", stubborn.cmd.ProcessState)
	}
	// The order the recorder sees them in need not be the order they were
	// sent.
	terms := stubborn.got(t, "SIGTERM")
	if len(terms) != 1 || len(stubborn.got(t, "SIGCONT")) != 1 {
		t.Errorf("the stubborn process got %q, want SIGTERM and SIGCONT once each", stubborn.signals(t))
	} else if waited := killed.Sub(terms[0]); waited < 1500*time.Millisecond {
		t.Errorf("SIGKILL came %v after SIGTERM, before the timeout of 1.5s", waited)
	}
	if got := quick.endedBy(); got != syscall.SIGTERM {
		t.Errorf("the quick process ended with %v, want SIGTERM", quick.cmd.ProcessState)
	}
	if code := k.code(); code != exitOK {
		t.Errorf("exit status %d, want %d", code, exitOK)
	}
	checkReports(t, "stdout", k.stdout(), stubborn.pid(), "sent SIGTERM", "sent SIGKILL after 1.5s")
	checkReports(t, "stdout", k.stdout(), slow.pid(), "sent SIGTERM", "exited")
	checkReports(t, "stdout", k.stdout(), quick.pid(), "sent SIGTERM", "exited")
	if k.stderr() != "" {
		t.Errorf("stderr = %q, want nothing", k.stderr())
	}
}

// The first signal is the one -s or KIL_SIGNAL chooses, SIGTERM if neither
// does; without --timeout, nothing waits and the default stays SIGKILL.
func TestTimeoutFirstSignal(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		args    []string
		header  string
		first   string
		reports []string
	}{
		{"KIL_SIGNAL", "KIL_SIGNAL=HUP", []string{"--timeout", "1s"}, "Enter SIGHUP (KILL after 1s)", "SIGHUP", []string{"sent SIGHUP", "sent SIGKILL after 1s"}},
		{"-s over KIL_SIGNAL", "KIL_SIGNAL=bogus", []string{"--timeout=1s", "-s", "HUP"}, "Enter SIGHUP (KILL after 1s)", "SIGHUP", []string{"sent SIGHUP", "sent SIGKILL after 1s"}},
		{"KIL_SIGNAL=15", "KIL_SIGNAL=15", []string{"--timeout", "1s"}, "Enter SIGTERM (KILL after 1s)", "SIGTERM", []string{"sent SIGTERM", "sent SIGKILL after 1s"}},
		{"no timeout", "KIL_SIGNAL=", nil, "Enter SIGKILL,", "", []string{"sent SIGKILL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
			p := startRecorder(t, tag)
			k := startKilWith(t, tt.env, append(tt.args, tag)...)
			k.enter(tt.header)
			k.waitExit(t, p, 5*time.Second)
			if got := p.endedBy(); got != syscall.SIGKILL {
				t.Errorf("the process ended with %v, want SIGKILL", p.cmd.ProcessState)
			}
			if tt.first != "" && len(p.got(t, tt.first)) != 1 {
				t.Errorf("the process got %q, want %s first", p.signals(t), tt.first)
			}
			if tt.first == "" && strings.Contains(p.signals(t), "SIG") {
				t.Errorf("without --timeout the process got %q before SIGKILL", p.signals(t))
			}
			if code := k.code(); code != exitOK {
				t.Errorf("exit status %d, want %d", code, exitOK)
			}
			checkReports(t, "stdout", k.stdout(), p.pid(), tt.reports...)
		})
	}
}

// A stopped job, as one suspended with Ctrl-Z, ends on SIGTERM thanks to
// SIGCONT, rather than wait for SIGKILL. A stop signal chosen with -s is not
// undone by SIGCONT.
func TestTimeoutAndStoppedProcesses(t *testing.T) {
	t.Run("stopped job", func(t *testing.T) {
		tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
		p := startPlain(t, tag)
		if err := p.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "5s", tag)
		k.enter("KILL after 5s")
		k.waitExit(t, p, 4*time.Second)
		if got := p.endedBy(); got != syscall.SIGTERM {
			t.Errorf("the process ended with %v, want SIGTERM", p.cmd.ProcessState)
		}
		if code := k.code(); code != exitOK {
			t.Errorf("exit status %d, want %d", code, exitOK)
		}
		checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM", "exited")
	})
	for _, sig := range []string{"STOP", "TSTP", "TTIN", "TTOU"} {
		t.Run("-s "+sig, func(t *testing.T) {
			tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
			p := startRecorder(t, tag)
			k := startKilWith(t, "KIL_SIGNAL=", "-s", sig, "--timeout", "1s", tag)
			k.enter("Enter SIG" + sig + " (KILL after 1s)")
			waitUntil(t, "the process to stop", func() bool { return procState(p.pid()) == "T" })
			time.Sleep(500 * time.Millisecond)
			if state := procState(p.pid()); state != "T" {
				t.Errorf("the process is in state %q after SIG%s, want it still stopped", state, sig)
			}
			k.waitExit(t, p, 5*time.Second)
			if got := p.endedBy(); got != syscall.SIGKILL {
				t.Errorf("the process ended with %v, want SIGKILL", p.cmd.ProcessState)
			}
			checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIG"+sig, "sent SIGKILL after 1s")
		})
	}
	t.Run("-s CONT", func(t *testing.T) {
		tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
		p := startRecorder(t, tag)
		k := startKilWith(t, "KIL_SIGNAL=", "-s", "CONT", "--timeout", "1s", tag)
		k.enter("Enter SIGCONT (KILL after 1s)")
		k.waitExit(t, p, 5*time.Second)
		if got := len(p.got(t, "SIGCONT")); got != 1 {
			t.Errorf("the process got SIGCONT %d times, want once: %q", got, p.signals(t))
		}
		checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGCONT", "sent SIGKILL after 1s")
	})
}

func procState(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ""
	}
	return strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))[0]
}

// startZombieParent starts a parent named by parentTag whose child, named
// by tag, it never reaps; the child records signals if record is set.
func startZombieParent(t *testing.T, parentTag, tag string, record bool) (parent *exec.Cmd, child *target) {
	t.Helper()
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "pid")
	parent = exec.Command(os.Args[0], "-test.run=^TestZombieParentChild$", parentTag)
	parent.Env = append(os.Environ(), "KIL_TEST_ZOMBIE_PARENT=1", "KIL_TEST_ZOMBIE_TAG="+tag, "KIL_TEST_ZOMBIE_PIDFILE="+pidfile)
	child = &target{}
	if record {
		child.log = filepath.Join(dir, "signals")
		parent.Env = append(parent.Env, "KIL_TEST_ZOMBIE_LOG="+child.log)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	t.Cleanup(func() {
		if pid > 0 {
			_ = unix.Kill(pid, unix.SIGKILL)
		}
		_ = parent.Process.Kill()
		_ = parent.Wait()
	})
	waitUntil(t, "the parent to start its child", func() bool {
		b, err := os.ReadFile(pidfile)
		if err == nil {
			pid, _ = strconv.Atoi(string(b))
		}
		return pid > 0
	})
	child.cmd = &exec.Cmd{Process: &os.Process{Pid: pid}}
	waitUntil(t, "the child to start", func() bool {
		if record {
			return strings.HasPrefix(child.signals(t), "ready")
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		return err == nil && strings.Contains(string(b), tag)
	})
	return parent, child
}

// A process that ended but whose parent has not reaped it is a zombie,
// which SIGKILL cannot end; kil takes it as exited.
func TestTimeoutTakesAZombieAsExited(t *testing.T) {
	tag := fmt.Sprintf("kilzombie%d", time.Now().UnixNano())
	_, child := startZombieParent(t, "kilzparent"+tag, tag, false)
	k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "5s", tag)
	k.enter("KILL after 5s")
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitOK {
		t.Errorf("exit status %d, want %d", code, exitOK)
	}
	if state := procState(child.pid()); state != "Z" {
		t.Errorf("the process is in state %q, want a zombie", state)
	}
	checkReports(t, "stdout", k.stdout(), child.pid(), "sent SIGTERM", "exited")
}

// A child selected with its parent is still sent SIGKILL after the parent
// exits and it is reparented, as its line differs only in the PPID.
func TestTimeoutKillsAReparentedChild(t *testing.T) {
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	parent, child := startZombieParent(t, "kilparent"+tag, "kilchild"+tag, true)
	k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1s", tag)
	k.waitFor(0, "KILL after 1s")
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	k.enter("KILL after 1s")
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitOK {
		t.Errorf("exit status %d, want %d", code, exitOK)
	}
	// The child's new parent may not have reaped it yet.
	waitUntil(t, "the child to end", func() bool { s := procState(child.pid()); return s == "" || s == "Z" })
	checkReports(t, "stdout", k.stdout(), parent.Process.Pid, "sent SIGTERM", "exited")
	checkReports(t, "stdout", k.stdout(), child.pid(), "sent SIGTERM", "sent SIGKILL after 1s")
}

// A process whose line changes while kil waits, as one that execs, may no
// longer be the process the user chose, so it is not sent SIGKILL.
func TestTimeoutSkipsAChangedProcess(t *testing.T) {
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	p := startRecorder(t, tag, "KIL_TEST_EXEC_ON_TERM=1")
	k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1s", tag)
	k.enter("KILL after 1s")
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitProblems {
		t.Errorf("exit status %d, want %d", code, exitProblems)
	}
	if !p.alive() {
		t.Errorf("the changed process ended with %v, want it left running", p.cmd.ProcessState)
	}
	checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM")
	checkReports(t, "stderr", k.stderr(), p.pid(), "skipped: process changed, not sent SIGKILL")
}

// SIGINT, SIGTERM, or SIGQUIT while kil waits, as Ctrl-C on the terminal
// kil gave back, stops it before SIGKILL, as it would kill(1); SIGHUP does
// not.
func TestSignalWhileWaiting(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP} {
		t.Run(unix.SignalName(sig), func(t *testing.T) {
			tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
			p := startRecorder(t, tag)
			k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "3s", tag)
			k.enter("KILL after 3s")
			waitUntil(t, "SIGTERM to arrive", func() bool { return len(p.got(t, "SIGTERM")) == 1 })
			time.Sleep(300 * time.Millisecond)
			if err := unix.Kill(k.pid, sig); err != nil {
				t.Fatal(err)
			}
			if sig == syscall.SIGHUP {
				k.waitExit(t, p, 5*time.Second)
				if code := k.code(); code != exitOK {
					t.Errorf("exit status %d, want %d", code, exitOK)
				}
				checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM", "sent SIGKILL after 3s")
				return
			}
			// kil stops at once, not when the timeout would have passed.
			signalled := time.Now()
			waitUntil(t, "kil to end", k.ended)
			if took := time.Since(signalled); took > 1500*time.Millisecond {
				t.Errorf("kil took %v to stop after %v", took, sig)
			}
			if code := k.code(); code != 128+int(sig) {
				t.Errorf("exit status %d, want %d", code, 128+int(sig))
			}
			if !p.alive() {
				t.Errorf("the process ended with %v, want it left running", p.cmd.ProcessState)
			}
			checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM")
			checkReports(t, "stderr", k.stderr(), p.pid(), "skipped: not sent SIGKILL")
		})
	}
}

// partialEnter selects every row, shrinks the terminal to one row, and
// presses Enter, which signals only the row on screen, and returns once one
// target got the first signal.
func partialEnter(t *testing.T, k *ptyKil, targets []*target) *target {
	t.Helper()
	k.waitFor(0, "KILL after")
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	time.Sleep(500 * time.Millisecond)
	if err := unix.IoctlSetWinsize(int(k.master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 4, Col: 100}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	k.send("\r")
	var first *target
	waitUntil(t, "one process to get SIGTERM", func() bool {
		n := 0
		for _, p := range targets {
			if len(p.got(t, "SIGTERM")) > 0 {
				n, first = n+1, p
			}
		}
		return n > 0
	})
	time.Sleep(1500 * time.Millisecond)
	n := 0
	for _, p := range targets {
		n += len(p.got(t, "SIGTERM"))
		if !p.alive() {
			t.Fatalf("PID %d ended while kil was still picking", p.pid())
		}
	}
	if n != 1 || k.ended() {
		t.Fatalf("after a partial Enter, %d processes got SIGTERM and kil ended=%v; want 1 and kil still open", n, k.ended())
	}
	return first
}

// Processes signalled by a partial Enter are waited for, however the
// picker then ends, for the whole timeout counted from when it ends.
func TestTimeoutAfterAPartialEnter(t *testing.T) {
	tests := []struct {
		name string
		// end ends the picker, the last key at the time it returns.
		end  func(k *ptyKil) time.Time
		code int
		// all says whether Enter signalled the rest.
		all bool
	}{
		{"Enter again", func(k *ptyKil) time.Time {
			time.Sleep(700 * time.Millisecond)
			k.send("\r")
			time.Sleep(700 * time.Millisecond)
			k.send("\r")
			return time.Now()
		}, exitOK, true},
		{"Esc", func(k *ptyKil) time.Time {
			k.send("\x1b")
			time.Sleep(300 * time.Millisecond)
			k.send("y")
			return time.Now()
		}, exitProblems, false},
		{"Ctrl-C", func(k *ptyKil) time.Time { k.send("\x03"); return time.Now() }, exitAborted, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
			var targets []*target
			for range 3 {
				targets = append(targets, startRecorder(t, tag))
			}
			k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1s", tag)
			first := partialEnter(t, k, targets)
			ended := tt.end(k)
			killed := k.waitExit(t, first, 5*time.Second)
			// The first one was signalled more than a second before the
			// picker ended, so a timeout counted from its signal would end
			// it at once.
			if waited := killed.Sub(ended); waited < time.Second {
				t.Errorf("SIGKILL came %v after the picker ended, before the timeout of 1s", waited)
			}
			for _, p := range targets {
				if p == first || tt.all {
					k.waitExit(t, p, 5*time.Second)
				}
			}
			waitUntil(t, "kil to end", k.ended)
			if code := k.code(); code != tt.code {
				t.Errorf("exit status %d, want %d", code, tt.code)
			}
			for _, p := range targets {
				if p == first || tt.all {
					if p.endedBy() != syscall.SIGKILL {
						t.Errorf("PID %d: %v, want it ended by SIGKILL", p.pid(), p.cmd.ProcessState)
					}
					checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM", "sent SIGKILL after 1s")
					continue
				}
				if !p.alive() || len(p.got(t, "SIGTERM")) != 0 {
					t.Errorf("PID %d was signalled, want it left alone; it got %q", p.pid(), p.signals(t))
				}
				checkReports(t, "stdout", k.stdout(), p.pid())
			}
		})
	}
}

// A signal that ends the picker after a partial Enter, even SIGHUP, ends
// kil without waiting, and says which processes were not sent SIGKILL.
func TestSignalAfterAPartialEnter(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(unix.SignalName(sig), func(t *testing.T) {
			tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
			var targets []*target
			for range 3 {
				targets = append(targets, startRecorder(t, tag))
			}
			k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1s", tag)
			first := partialEnter(t, k, targets)
			if err := unix.Kill(k.pid, sig); err != nil {
				t.Fatal(err)
			}
			if code := k.code(); code != 128+int(sig) {
				t.Errorf("exit status %d, want %d", code, 128+int(sig))
			}
			time.Sleep(1500 * time.Millisecond)
			if !first.alive() {
				t.Errorf("the process signalled first ended with %v, want it left running", first.cmd.ProcessState)
			}
			checkReports(t, "stdout", k.stdout(), first.pid(), "sent SIGTERM")
			checkReports(t, "stderr", k.stderr(), first.pid(), "skipped: not sent SIGKILL")
		})
	}
}

// A ps that fails while kil waits is a failure, not an exit, and the
// process is not sent SIGKILL.
func TestTimeoutWhenTheCheckFails(t *testing.T) {
	real, err := exec.LookPath("ps")
	if err != nil {
		t.Skip("no ps")
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken")
	script := "#!/bin/sh\nif [ -e '" + broken + "' ]; then echo 'ps: broken' >&2; exit 1; fi\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	p := startRecorder(t, tag)
	k := startKil(t, "KIL_SIGNAL=", "KIL_EXACT=1", "KIL_TEST_SPLIT=1", "KIL_TEST_ARGS=--timeout\n1s\n"+tag, "PATH="+dir+":"+os.Getenv("PATH"))
	k.enter("KILL after 1s")
	waitUntil(t, "SIGTERM to arrive", func() bool { return len(p.got(t, "SIGTERM")) == 1 })
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitProblems {
		t.Errorf("exit status %d, want %d", code, exitProblems)
	}
	time.Sleep(300 * time.Millisecond)
	if !p.alive() {
		t.Errorf("the process ended with %v, want it left running", p.cmd.ProcessState)
	}
	checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM")
	checkReports(t, "stderr", k.stderr(), p.pid(), "failed: checking the process: ps failed: exit status 1: ps: broken")
}

// A process that exited before Enter is reported as skipped, as without
// --timeout, and is neither waited for nor sent SIGKILL, while the others
// are.
func TestTimeoutSkipsWhatWasNotSignalled(t *testing.T) {
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	gone := startRecorder(t, tag)
	stays := startRecorder(t, tag)
	k := startKilWith(t, "KIL_SIGNAL=", "--timeout", "1s", tag)
	k.waitFor(0, "KILL after 1s")
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	_ = gone.cmd.Process.Kill()
	<-gone.exited
	k.enter("KILL after 1s")
	k.waitExit(t, stays, 5*time.Second)
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitProblems {
		t.Errorf("exit status %d, want %d", code, exitProblems)
	}
	checkReports(t, "stdout", k.stdout(), gone.pid())
	checkReports(t, "stderr", k.stderr(), gone.pid(), "skipped: process changed or exited")
	checkReports(t, "stdout", k.stdout(), stays.pid(), "sent SIGTERM", "sent SIGKILL after 1s")
}

// procps gives a start time that can differ by a second between runs, as it
// adds the uptime it reads to the clock, so a start time that moves alone
// must not make kil take a process for another one.
func TestTimeoutWithAStartTimeThatMoves(t *testing.T) {
	real, err := exec.LookPath("ps")
	if err != nil {
		t.Skip("no ps")
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	// The wrapper adds a number of seconds that grows with every run to
	// the start time, wherever it is asked for.
	script := `#!/bin/sh
case "$*" in
*lstart*)
	n=$(cat '` + count + `' 2>/dev/null || echo 0); n=$((n + 1)); echo $n > '` + count + `'
	'` + real + `' "$@" | sed "s/\$/ +$n/" ;;
*)
	exec '` + real + `' "$@" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("kilto%d", time.Now().UnixNano())
	p := startRecorder(t, tag)
	k := startKil(t, "KIL_SIGNAL=", "KIL_EXACT=1", "KIL_TEST_SPLIT=1", "KIL_TEST_ARGS=--timeout\n1s\n"+tag, "PATH="+dir+":"+os.Getenv("PATH"))
	k.enter("KILL after 1s")
	k.waitExit(t, p, 5*time.Second)
	if code := k.code(); code != exitOK {
		t.Errorf("exit status %d, want %d", code, exitOK)
	}
	checkReports(t, "stdout", k.stdout(), p.pid(), "sent SIGTERM", "sent SIGKILL after 1s")
}
