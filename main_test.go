package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/ui"
)

func runKil(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	getenv := func(k string) string { return env[k] }
	code := run(args, getenv, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		code, out, _ := runKil(t, nil, arg)
		if code != 0 || !strings.Contains(out, "Usage: kil") {
			t.Errorf("kil %s = %d, %q; want 0 and usage", arg, code, out)
		}
	}
}

func TestUnknownOptionIsAUsageError(t *testing.T) {
	code, _, errOut := runKil(t, nil, "-x")
	if code != 2 || !strings.Contains(errOut, "kil -- -x") {
		t.Errorf("kil -x = %d, %q; want 2 and a hint to use --", code, errOut)
	}
}

func TestInvalidSignalIsRejectedBeforeListing(t *testing.T) {
	code, _, errOut := runKil(t, map[string]string{"KIL_SIGNAL": "-9"})
	if code != 2 || !strings.Contains(errOut, "KIL_SIGNAL") {
		t.Errorf("KIL_SIGNAL=-9 kil = %d, %q; want 2 and a KIL_SIGNAL error", code, errOut)
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args  []string
		query string
	}{
		{nil, ""},
		{[]string{"node"}, "node"},
		{[]string{"node", "dev"}, "node dev"},
		{[]string{"--", "-zsh"}, "-zsh"},
		{[]string{"--", "--help"}, "--help"},
		{[]string{"--", "-e"}, "-e"},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil || got.query != tt.query || got.help || got.exact {
			t.Errorf("parseArgs(%q) = %+v, %v; want query %q", tt.args, got, err, tt.query)
		}
	}
}

func TestExactOption(t *testing.T) {
	for _, args := range [][]string{{"-e", "ruby"}, {"--exact", "ruby"}, {"-e", "--", "ruby"}} {
		got, err := parseArgs(args)
		if err != nil || !got.exact || got.query != "ruby" {
			t.Errorf("parseArgs(%q) = %+v, %v; want exact with query \"ruby\"", args, got, err)
		}
	}
}

func TestExactFromEnvironment(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{"true", true},
	}
	for _, tt := range tests {
		got, err := exactFromEnv(tt.value)
		if err != nil || got != tt.want {
			t.Errorf("exactFromEnv(%q) = %v, %v; want %v", tt.value, got, err, tt.want)
		}
	}
	if _, err := exactFromEnv("yes please"); err == nil {
		t.Error("exactFromEnv(\"yes please\") succeeded, want an error")
	}
}

func TestInvalidExactEnvironmentIsRejected(t *testing.T) {
	code, _, errOut := runKil(t, map[string]string{"KIL_EXACT": "maybe"})
	if code != 2 || !strings.Contains(errOut, "KIL_EXACT") {
		t.Errorf("KIL_EXACT=maybe kil = %d, %q; want 2 and a KIL_EXACT error", code, errOut)
	}
}

// When KIL_TEST_BROKEN_PIPE is set, this test is the child process of
// TestClosedStdoutDoesNotStopSignalling: it reports results to a stdout
// whose reader has exited, as `kil | head -n 0` does.
func TestBrokenPipeChild(t *testing.T) {
	if os.Getenv("KIL_TEST_BROKEN_PIPE") == "" {
		t.Skip("run by TestClosedStdoutDoesNotStopSignalling")
	}
	ignoreBrokenPipe()
	for range 3 {
		reportln(os.Stdout, "sent SIGKILL: a process")
	}
	reportln(os.Stderr, "all results reported")
}

// Go ends a program that writes to a broken pipe on stdout with SIGPIPE,
// which would leave the remaining selected processes unsignalled.
func TestClosedStdoutDoesNotStopSignalling(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrokenPipeChild$")
	cmd.Env = append(os.Environ(), "KIL_TEST_BROKEN_PIPE=1")
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	_ = w.Close()
	if runErr != nil || !strings.Contains(stderr.String(), "all results reported") {
		t.Errorf("child did not report every result and exit cleanly: %v\n%s", runErr, stderr.String())
	}
}

// Esc and Ctrl-C are how the user leaves kil on purpose, so they end
// quietly, as fzf does, with only the exit status telling a script.
func TestAbortEndsWithoutAMessage(t *testing.T) {
	for _, k := range []ui.KeyPressMsg{{Code: ui.KeyEscape}, {Code: 'c', Mod: ui.ModCtrl}} {
		next, _ := ui.New(nil, "", "SIGKILL", nil).Update(k)
		var stdout, stderr bytes.Buffer
		code := finish(next, syscall.SIGKILL, &batches{}, &stdout, &stderr, nil)
		if code != exitAborted || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("%v: finish = %d, stdout %q, stderr %q; want %d and no output", k, code, stdout.String(), stderr.String(), exitAborted)
		}
	}
}

// Killing a parent reparents its children, which changes their PPID. That is
// still the same process, so a child selected after its parent must be
// signalled, not skipped as changed.
func TestChildSelectedAfterItsParentIsSignalled(t *testing.T) {
	parent := exec.Command("sh", "-c", "sleep 1000 & echo $!; wait")
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	if _, err := fmt.Fscan(out, &child); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(child, syscall.SIGKILL)
		_ = parent.Process.Kill()
		_ = parent.Wait()
	})

	procs, err := proc.List(0)
	if err != nil {
		t.Fatal(err)
	}
	var targets []picker.Item
	for _, pid := range []int{parent.Process.Pid, child} {
		for _, p := range procs {
			if p.PID == pid {
				targets = append(targets, picker.Item{ID: p.Identity, Text: p.Raw})
			}
		}
	}
	if len(targets) != 2 {
		t.Fatalf("parent and child not both listed: %v", targets)
	}

	var stdout, stderr bytes.Buffer
	if code := signalAll(targets, syscall.SIGKILL, &stdout, &stderr, nil); code != exitOK {
		t.Errorf("signalAll = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, exitOK, stdout.String(), stderr.String())
	}
}

// Reports of the rows a partial Enter signalled are held while the picker
// owns the screen, then printed when kil ends, with the selections left
// unsignalled reported as skipped. The sender here only records, so no
// process is signalled.
func TestStoppedReportsEarlierBatchesAndTheSelectionsLeft(t *testing.T) {
	var b batches
	var its []picker.Item
	for i := range 30 {
		line := fmt.Sprintf("%03d proc", 100+i)
		its = append(its, picker.Item{ID: line, Text: line})
	}
	press := func(m ui.Model, msgs ...ui.Msg) ui.Model {
		for _, msg := range msgs {
			next, _ := m.Update(msg)
			m = next
		}
		return m
	}
	m := ui.New(its, "", "SIGKILL", nil).WithSender(func(batch []picker.Item) {
		for _, it := range batch {
			reportf(b.stdout(), "sent SIGKILL: %s\n", it.Text)
		}
	})
	m = press(m,
		ui.WindowSizeMsg{Width: 80, Height: 24},
		m.Init()(), // the first frame is drawn
		ui.KeyPressMsg{Code: 'a', Mod: ui.ModAlt},
		ui.WindowSizeMsg{Width: 80, Height: 10},
		ui.KeyPressMsg{Code: ui.KeyEnter},
		ui.KeyPressMsg{Code: ui.KeyEscape},
		ui.KeyPressMsg{Code: 'y', Text: "y"},
	)
	if m.Outcome() != ui.Stopped {
		t.Fatalf("Outcome() = %v, want Stopped", m.Outcome())
	}
	var stdout, stderr bytes.Buffer
	code := finish(m, syscall.SIGKILL, &b, &stdout, &stderr, nil)
	if code != exitProblems {
		t.Errorf("finish = %d, want %d", code, exitProblems)
	}
	if !strings.Contains(stdout.String(), "sent SIGKILL: ") {
		t.Errorf("stdout = %q, want the earlier batch's reports", stdout.String())
	}
	if got, want := strings.Count(stderr.String(), "skipped: not signalled: "), len(m.Left()); got != want || want == 0 {
		t.Errorf("stderr reports %d selections left, want %d:\n%s", got, want, stderr.String())
	}
}

// A picker failure after a partial Enter must still print what was signalled.
func TestPickerFailureKeepsEarlierReports(t *testing.T) {
	var b batches
	reportln(b.stdout(), "sent SIGKILL: 101 a")
	var stdout, stderr bytes.Buffer
	code := pickFailed(&b, errors.New("read /dev/tty: input/output error"), &stdout, &stderr, nil)
	if code != exitError || !strings.Contains(stdout.String(), "sent SIGKILL: 101 a") || !strings.Contains(stderr.String(), "input/output error") {
		t.Errorf("pickFailed = %d, stdout %q, stderr %q; want %d with the earlier report and the error", code, stdout.String(), stderr.String(), exitError)
	}
}

// After a partial Enter some processes were signalled, so the failure must
// not claim that nothing was.
func TestFailureAfterAPartialEnterDoesNotClaimNothingWasSignalled(t *testing.T) {
	reload := func() ([]picker.Item, error) { return nil, errors.New("ps failed") }
	var its []picker.Item
	for i := range 30 {
		line := fmt.Sprintf("%03d proc", 100+i)
		its = append(its, picker.Item{ID: line, Text: line})
	}
	var b batches
	m := ui.New(its, "", "SIGKILL", reload).WithSender(func(batch []picker.Item) {
		for _, it := range batch {
			reportf(b.stdout(), "sent SIGKILL: %s\n", it.Text)
		}
		b.sent += len(batch)
	})
	var cmd ui.Cmd
	for _, msg := range []ui.Msg{
		ui.WindowSizeMsg{Width: 80, Height: 24},
		m.Init()(), // the first frame is drawn
		ui.KeyPressMsg{Code: 'a', Mod: ui.ModAlt},
		ui.WindowSizeMsg{Width: 80, Height: 10},
		ui.KeyPressMsg{Code: ui.KeyEnter},
		ui.KeyPressMsg{Code: 'r', Mod: ui.ModCtrl},
	} {
		var next ui.Model
		next, cmd = m.Update(msg)
		m = next
	}
	next, _ := m.Update(cmd())
	m = next
	if m.Outcome() != ui.ListFailed {
		t.Fatalf("Outcome() = %v, want ListFailed", m.Outcome())
	}
	var stdout, stderr bytes.Buffer
	finish(m, syscall.SIGKILL, &b, &stdout, &stderr, nil)
	if strings.Contains(stderr.String(), "nothing was signalled") || !strings.Contains(stderr.String(), "ps failed") {
		t.Errorf("stderr = %q, want the error without \"nothing was signalled\"", stderr.String())
	}
	if got, want := strings.Count(stderr.String(), "skipped: not signalled: "), 14; got != want {
		t.Errorf("stderr reports %d selections left, want %d:\n%s", got, want, stderr.String())
	}
}

// When stdout and stderr go to the same file, the reports held during the
// picker must come out in the order the processes were handled.
func TestHeldReportsKeepTheirOrder(t *testing.T) {
	var b batches
	reportln(b.stdout(), "sent SIGKILL: 101 a")
	reportln(b.stderr(), "skipped: process changed or exited: 102 b")
	reportln(b.stdout(), "sent SIGKILL: 103 c")
	var both bytes.Buffer
	pickFailed(&b, errors.New("picker failed"), &both, &both, nil)
	want := "sent SIGKILL: 101 a\nskipped: process changed or exited: 102 b\nsent SIGKILL: 103 c\nkil: picker failed\n"
	if both.String() != want {
		t.Errorf("output = %q, want %q", both.String(), want)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// `kil --help > /dev/full` must not report success when the usage was not
// written, as GNU tools do.
func TestHelpWriteErrorFails(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"--help"}, func(string) string { return "" }, failingWriter{}, &stderr)
	if code != exitError || !strings.Contains(stderr.String(), "no space left on device") {
		t.Errorf("kil --help to a full disk = %d, stderr %q; want %d and the error", code, stderr.String(), exitError)
	}
}

// Without TERM, or with TERM=dumb, the terminal may not show colors, so kil
// draws without them, as it did before it used tcell.
func TestScreenWithoutAKnownTermHasNoColors(t *testing.T) {
	for _, tt := range []struct {
		term       string
		monochrome bool
	}{{"", true}, {"dumb", true}, {"xterm-256color", false}, {"kil-unknown-term", false}} {
		s, err := newScreen(tt.term)
		if err != nil {
			t.Fatalf("TERM=%s: %v", tt.term, err)
		}
		if _, ok := s.(monochrome); ok != tt.monochrome {
			t.Errorf("TERM=%s: monochrome %v, want %v", tt.term, ok, tt.monochrome)
		}
	}
}

// --version prints kil's version from the build, as go install records it,
// so that a bug report can say which kil it is about.
func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, func(string) string { return "" }, &stdout, &stderr)
	v, ok := strings.CutPrefix(stdout.String(), "kil ")
	v, nl := strings.CutSuffix(v, "\n")
	if code != exitOK || !ok || !nl || v == "" || v == "(devel)" || strings.ContainsAny(v, " \n") || stderr.Len() != 0 {
		t.Errorf("kil --version = %d, stdout %q, stderr %q; want %d and \"kil <version>\"", code, stdout.String(), stderr.String(), exitOK)
	}
	if !strings.Contains(usage, "--version") {
		t.Error("the usage does not mention --version")
	}
}

// A release build gives its version with -ldflags, which --version prints
// as it is.
func TestVersionOfARelease(t *testing.T) {
	saved := releaseVersion
	t.Cleanup(func() { releaseVersion = saved })
	releaseVersion = "v1.2.3"
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, func(string) string { return "" }, &stdout, &stderr); code != exitOK || stdout.String() != "kil v1.2.3\n" {
		t.Errorf("kil --version = %d, %q; want %d and \"kil v1.2.3\\n\"", code, stdout.String(), exitOK)
	}
}

// After --, --version is a query, as --help is.
func TestVersionAfterDashDashIsAQuery(t *testing.T) {
	opts, err := parseArgs([]string{"--", "--version"})
	if err != nil || opts.version || opts.query != "--version" {
		t.Errorf("parseArgs(-- --version) = %+v, %v; want the query --version", opts, err)
	}
}
