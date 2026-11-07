package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPTY returns a new pseudo-terminal pair.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	return master, slave
}

// When KIL_TEST_HELP_CHILD is set, this test is the child process of
// TestHelpOnATerminalSendsNoQuery, running `kil --help` on a terminal.
func TestHelpChild(t *testing.T) {
	if os.Getenv("KIL_TEST_HELP_CHILD") == "" {
		t.Skip("run by TestHelpOnATerminalSendsNoQuery")
	}
	run([]string{"--help"}, os.Getenv, os.Stdout, os.Stderr)
}

// Bubble Tea v1 asked the terminal for its background color as the program
// started, even for --help: a terminal that does not answer kept kil waiting
// for 5 seconds, and an answer arriving later was typed into the query.
func TestHelpOnATerminalSendsNoQuery(t *testing.T) {
	master, slave := openPTY(t)
	defer func() { _ = master.Close() }()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelpChild$")
	cmd.Env = append(os.Environ(), "KIL_TEST_HELP_CHILD=1", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// The terminal is the child's controlling terminal, with the child in
	// its foreground, as when kil runs from a shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var out bytes.Buffer
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return // EIO once the child has closed the terminal
			}
		}
	}()
	select {
	case <-read:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("kil --help did not finish")
	}
	_ = cmd.Wait()
	elapsed := time.Since(start)
	if s := out.String(); strings.Contains(s, "\x1b]11;?") || strings.Contains(s, "\x1b[6n") {
		t.Errorf("kil --help queried the terminal: %q", s)
	}
	if elapsed > 2*time.Second {
		t.Errorf("kil --help took %v on a terminal that does not answer", elapsed)
	}
	if !strings.Contains(out.String(), "Usage: kil") {
		t.Errorf("no usage on the terminal: %q", out.String())
	}
}

// When KIL_TEST_SHELL is set, this test stands in for the interactive shell
// that kil is started from: it leads the session that owns the terminal and
// starts kil as a job in the terminal's foreground.
func TestShellChild(t *testing.T) {
	if os.Getenv("KIL_TEST_SHELL") == "" {
		t.Skip("run by the tests of kil on a terminal")
	}
	kil := exec.Command(os.Args[0], "-test.run=^TestKilChild$")
	kil.Env = append(os.Environ(), "KIL_TEST_SHELL=", "KIL_TEST_KIL=1")
	kil.Stdin, kil.Stdout, kil.Stderr = os.Stdin, os.Stdout, os.Stderr
	kil.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	if err := kil.Start(); err != nil {
		t.Fatal(err)
	}
	_ = kil.Wait()
	os.Exit(0)
}

// When KIL_TEST_KIL is set, this test is kil, started by TestShellChild. It
// records its PID and exit status, and its reports, in KIL_TEST_DIR.
func TestKilChild(t *testing.T) {
	if os.Getenv("KIL_TEST_KIL") == "" {
		t.Skip("run by TestShellChild")
	}
	dir := os.Getenv("KIL_TEST_DIR")
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			panic(err)
		}
	}
	write("pid", strconv.Itoa(os.Getpid()))
	stdout, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		panic(err)
	}
	code := run(nil, os.Getenv, stdout, stdout)
	write("code", strconv.Itoa(code))
	os.Exit(code)
}

// When KIL_TEST_SLEEPER is set, this test is a process for kil to signal.
func TestSleeperChild(t *testing.T) {
	if os.Getenv("KIL_TEST_SLEEPER") == "" {
		t.Skip("run by TestKillingTheShellDoesNotStopTheRest")
	}
	time.Sleep(time.Minute)
}

// ptyKil is kil running on a pseudo-terminal, started from a stand-in shell.
type ptyKil struct {
	t      *testing.T
	master *os.File
	dir    string
	tag    string
	shell  *exec.Cmd
	pid    int

	// initial is the terminal's mode before kil started, which kil must
	// leave it in.
	initial *unix.Termios

	mu  sync.Mutex
	out bytes.Buffer
}

// startKil starts kil on a 100x24 terminal from a stand-in shell, with env
// added to its environment. KIL_SIGNAL is 0 unless env sets it, so that a
// test that goes wrong signals nothing.
func startKil(t *testing.T, env ...string) *ptyKil {
	t.Helper()
	return launch(t, true, env)
}

// startKilAlone starts kil as startKil does but without a shell, as a
// terminal emulator or tmux runs a command: kil leads the terminal's
// session, and no shell can bring it back if it stops.
func startKilAlone(t *testing.T, env ...string) *ptyKil {
	t.Helper()
	return launch(t, false, env)
}

func launch(t *testing.T, withShell bool, env []string) *ptyKil {
	t.Helper()
	master, slave := openPTY(t)
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100}); err != nil {
		t.Fatal(err)
	}
	initial, err := unix.IoctlGetTermios(int(master.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	k := &ptyKil{t: t, master: master, dir: dir, tag: fmt.Sprintf("kiltest%d", time.Now().UnixNano()), initial: initial}
	// The tag names the shell in the process list.
	k.shell = exec.Command(os.Args[0], "-test.run=^TestShellChild$", "kilshell-"+k.tag)
	k.shell.Env = append(os.Environ(), "KIL_TEST_SHELL=1")
	if !withShell {
		k.shell = exec.Command(os.Args[0], "-test.run=^TestKilChild$")
		k.shell.Env = append(os.Environ(), "KIL_TEST_KIL=1")
	}
	k.shell.Env = append(k.shell.Env, "KIL_TEST_DIR="+dir, "TERM=xterm-256color", "KIL_SIGNAL=0", "KIL_EXACT=")
	k.shell.Env = append(k.shell.Env, env...)
	k.shell.Stdin, k.shell.Stdout, k.shell.Stderr = slave, slave, slave
	k.shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := k.shell.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			k.mu.Lock()
			k.out.Write(buf[:n])
			k.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(k.stop)
	k.eventually("kil to start", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "pid"))
		if err != nil {
			return false
		}
		k.pid, err = strconv.Atoi(string(b))
		return err == nil
	})
	k.waitFor(0, "Alt-H help")
	return k
}

// stop ends the processes the test started, if they are still running.
func (k *ptyKil) stop() {
	// The PID is checked to still be this test's kil, by its environment,
	// before it is signalled.
	environ, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", k.pid))
	if err == nil && slices.Contains(strings.Split(string(environ), "\x00"), "KIL_TEST_DIR="+k.dir) {
		_ = unix.Kill(k.pid, unix.SIGKILL)
	}
	_ = k.shell.Process.Kill()
	_ = k.shell.Wait()
	_ = k.master.Close()
}

func (k *ptyKil) eventually(what string, ok func() bool) {
	k.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			k.t.Fatalf("timed out waiting for %s; terminal output:\n%q", what, k.output(0))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// output returns what kil wrote to the terminal from offset from.
func (k *ptyKil) output(from int) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.out.String()[from:]
}

// mark returns the offset of what kil writes next.
func (k *ptyKil) mark() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.out.Len()
}

func (k *ptyKil) waitFor(from int, s string) {
	k.t.Helper()
	k.eventually(fmt.Sprintf("%q on the terminal", s), func() bool { return strings.Contains(k.output(from), s) })
}

func (k *ptyKil) send(s string) {
	k.t.Helper()
	if _, err := k.master.WriteString(s); err != nil {
		k.t.Fatal(err)
	}
}

// code waits for kil to exit and returns its exit status.
func (k *ptyKil) code() int {
	k.t.Helper()
	var code int
	k.eventually("kil to exit", func() bool {
		b, err := os.ReadFile(filepath.Join(k.dir, "code"))
		if err != nil {
			return false
		}
		code, err = strconv.Atoi(string(b))
		return err == nil
	})
	return code
}

// state returns kil's process state, such as "T" when it is stopped.
func (k *ptyKil) state() string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", k.pid))
	if err != nil {
		return ""
	}
	// The command name in parentheses may contain spaces.
	fields := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return fields[0]
}

// restored waits for kil to leave the alternate screen, written after
// output from, and reports whether the terminal is back in its mode from
// before kil started.
func (k *ptyKil) restored(from int) bool {
	k.t.Helper()
	k.waitFor(from, "\x1b[?1049l")
	tios, err := unix.IoctlGetTermios(int(k.master.Fd()), unix.TCGETS)
	if err != nil {
		k.t.Fatal(err)
	}
	return tios.Iflag == k.initial.Iflag && tios.Oflag == k.initial.Oflag &&
		tios.Cflag == k.initial.Cflag && tios.Lflag == k.initial.Lflag && tios.Cc == k.initial.Cc
}

// cooked reports whether the terminal has line editing and echo, as a
// shell expects and kil turns off while it runs.
func (k *ptyKil) cooked() bool {
	k.t.Helper()
	tios, err := unix.IoctlGetTermios(int(k.master.Fd()), unix.TCGETS)
	if err != nil {
		k.t.Fatal(err)
	}
	return tios.Lflag&unix.ICANON != 0 && tios.Lflag&unix.ECHO != 0
}

// Signals from outside, such as `kill <pid>` or a closed terminal, end kil
// with 128 plus the signal's number, as a shell reports, and leave the
// terminal as it was.
func TestSignalEndsKilAndRestoresTheTerminal(t *testing.T) {
	for s, want := range map[syscall.Signal]int{syscall.SIGHUP: 129, syscall.SIGINT: 130, syscall.SIGQUIT: 131, syscall.SIGTERM: 143} {
		t.Run(s.String(), func(t *testing.T) {
			k := startKil(t)
			from := k.mark()
			if err := unix.Kill(k.pid, s); err != nil {
				t.Fatal(err)
			}
			if code := k.code(); code != want {
				t.Errorf("exit status %d, want %d", code, want)
			}
			if !k.restored(from) {
				t.Errorf("terminal not restored; output %q", k.output(from))
			}
		})
	}
}

// Ctrl-Z, or SIGTSTP from outside, stops kil as a shell job with the
// terminal restored for the shell, and fg brings kil back as it was.
func TestSuspendAndResume(t *testing.T) {
	for name, suspend := range map[string]func(k *ptyKil){
		"Ctrl-Z":  func(k *ptyKil) { k.send("\x1a") },
		"SIGTSTP": func(k *ptyKil) { _ = unix.Kill(k.pid, unix.SIGTSTP) },
	} {
		t.Run(name, func(t *testing.T) {
			k := startKil(t)
			from := k.mark()
			suspend(k)
			k.eventually("kil to stop", func() bool { return k.state() == "T" })
			if !k.restored(from) {
				t.Errorf("terminal not restored while kil is stopped; output %q", k.output(from))
			}
			// The shell's fg: kil's job is still in the foreground.
			from = k.mark()
			if err := unix.Kill(k.pid, unix.SIGCONT); err != nil {
				t.Fatal(err)
			}
			k.waitFor(from, "Alt-H help")
			if k.cooked() {
				t.Error("terminal still cooked after kil resumed, so keys would wait for a newline")
			}
			k.send("\x03")
			if code := k.code(); code != exitAborted {
				t.Errorf("Ctrl-C after resuming: exit status %d, want %d", code, exitAborted)
			}
		})
	}
}

// Killing the shell kil was started from hangs up the terminal, which sends
// kil SIGHUP. kil must still signal the rest of the selection.
func TestKillingTheShellDoesNotStopTheRest(t *testing.T) {
	k := startKil(t, "KIL_SIGNAL=KILL", "KIL_EXACT=1")
	sleeper := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$", "kilsleep-"+k.tag)
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

	// The shell is selected first, so it is signalled first. The process
	// list is read again so that it has the sleeper.
	k.send("\x12")
	time.Sleep(500 * time.Millisecond)
	k.send("kilshell-" + k.tag)
	time.Sleep(500 * time.Millisecond)
	k.send("\t\x15kilsleep-" + k.tag)
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	time.Sleep(500 * time.Millisecond)
	k.send("\r")

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		out, _ := os.ReadFile(filepath.Join(k.dir, "stdout"))
		t.Fatalf("the process selected after the shell was not signalled; kil reported:\n%s", out)
	}
	if code := k.code(); code != exitOK {
		out, _ := os.ReadFile(filepath.Join(k.dir, "stdout"))
		t.Errorf("exit status %d, want %d; kil reported:\n%s", code, exitOK, out)
	}
}

// Started without a shell, kil could not be brought back once stopped, so
// Ctrl-Z and SIGTSTP leave it running.
func TestSuspendWithoutAShellDoesNothing(t *testing.T) {
	for name, suspend := range map[string]func(k *ptyKil){
		"Ctrl-Z":  func(k *ptyKil) { k.send("\x1a") },
		"SIGTSTP": func(k *ptyKil) { _ = unix.Kill(k.pid, unix.SIGTSTP) },
	} {
		t.Run(name, func(t *testing.T) {
			k := startKilAlone(t)
			suspend(k)
			time.Sleep(500 * time.Millisecond)
			if state := k.state(); state == "T" || k.cooked() {
				t.Errorf("kil without a shell: state %q, cooked %v; want it running in raw mode", state, k.cooked())
			}
			k.send("\x03")
			if code := k.code(); code != exitAborted {
				t.Errorf("Ctrl-C: exit status %d, want %d", code, exitAborted)
			}
		})
	}
}

// SIGTERM while kil is stopped ends it once it continues, with the
// terminal restored.
func TestSignalWhileStopped(t *testing.T) {
	k := startKil(t)
	k.send("\x1a")
	k.eventually("kil to stop", func() bool { return k.state() == "T" })
	if err := unix.Kill(k.pid, unix.SIGTERM); err != nil {
		t.Fatal(err)
	}
	from := k.mark()
	if err := unix.Kill(k.pid, unix.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if code := k.code(); code != 143 {
		t.Errorf("exit status %d, want 143", code)
	}
	if !k.restored(from) {
		t.Errorf("terminal not restored; output %q", k.output(from))
	}
}

// Without a TERM that tcell knows, or with TERM=dumb, kil draws as on xterm,
// as it did before it used tcell, rather than refuse to start.
func TestUnknownTermStillStarts(t *testing.T) {
	for _, term := range []string{"", "dumb", "kil-unknown-term"} {
		t.Run("TERM="+term, func(t *testing.T) {
			k := startKil(t, "TERM="+term)
			k.send("\x03")
			if code := k.code(); code != exitAborted {
				t.Errorf("Ctrl-C: exit status %d, want %d", code, exitAborted)
			}
		})
	}
}
