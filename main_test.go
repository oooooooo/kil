package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/port"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/ui"
	"github.com/oooooooo/kil/internal/width"
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

func TestSignalOptionIsInTheUsage(t *testing.T) {
	_, out, _ := runKil(t, nil, "--help")
	if !strings.Contains(out, "-s, --signal SIG") || !strings.Contains(out, "overrides KIL_SIGNAL") {
		t.Errorf("kil --help = %q; want -s, --signal SIG listed as overriding KIL_SIGNAL", out)
	}
}

// fakePS puts a ps on PATH that records each run in the returned file and
// fails, so that run returns after listing without a terminal.
func fakePS(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runs := dir + "/runs"
	script := "#!/bin/sh\necho run >> '" + runs + "'\necho 'ps: fake failure' >&2\nexit 1\n"
	if err := os.WriteFile(dir+"/ps", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return runs
}

// An invalid or missing -s value is rejected before the process list is
// read, naming the option as typed.
func TestInvalidSignalOptionIsRejectedBeforeListing(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-s"}, "kil: -s needs a signal"},
		{[]string{"--signal"}, "kil: --signal needs a signal"},
		{[]string{"-s", "bogus"}, `kil: -s: invalid signal "bogus"`},
		{[]string{"-s", ""}, `kil: -s: invalid signal ""`},
		{[]string{"--signal="}, `kil: --signal: invalid signal ""`},
		{[]string{"--signal=-9"}, `kil: --signal: invalid signal "-9"`},
		{[]string{"-s", "--", "node"}, `kil: -s: invalid signal "--"`},
		{[]string{"-s", "bogus", "-s", "TERM"}, `kil: -s: invalid signal "bogus"`},
	}
	for _, tt := range tests {
		runs := fakePS(t)
		code, _, errOut := runKil(t, map[string]string{"KIL_SIGNAL": "TERM"}, tt.args...)
		if code != exitError || !strings.HasPrefix(errOut, tt.want) {
			t.Errorf("kil %q = %d, %q; want %d and an error starting %q", tt.args, code, errOut, exitError, tt.want)
		}
		if _, err := os.Stat(runs); !os.IsNotExist(err) {
			t.Errorf("kil %q ran ps before rejecting the option, or its record is unreadable: %v", tt.args, err)
		}
	}
}

// With -s, KIL_SIGNAL is not read, so an invalid one cannot stop kil.
func TestSignalOptionDoesNotReadTheEnvironment(t *testing.T) {
	runs := fakePS(t)
	var asked []string
	getenv := func(k string) string {
		asked = append(asked, k)
		if k == "KIL_SIGNAL" {
			return "bogus"
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-s", "TERM"}, getenv, &stdout, &stderr)
	if slices.Contains(asked, "KIL_SIGNAL") || strings.Contains(stderr.String(), "KIL_SIGNAL") {
		t.Errorf("kil -s TERM read KIL_SIGNAL; it reported %q", stderr.String())
	}
	if _, err := os.Stat(runs); err != nil || code != exitError || !strings.Contains(stderr.String(), "failed to list processes") {
		t.Errorf("kil -s TERM = %d, %q; want it to go on to list processes", code, stderr.String())
	}
}

func TestSignalOptionWithOtherArguments(t *testing.T) {
	tests := []struct {
		args   []string
		signal syscall.Signal
		exact  bool
		query  string
	}{
		{[]string{"-s", "TERM", "node"}, syscall.SIGTERM, false, "node"},
		{[]string{"-e", "-s", "HUP", "node", "dev"}, syscall.SIGHUP, true, "node dev"},
		{[]string{"-s", "TERM", "-e", "node"}, syscall.SIGTERM, true, "node"},
		{[]string{"--signal=15", "--", "-zsh"}, syscall.SIGTERM, false, "-zsh"},
		{[]string{"-s", "KILL", "--signal", "TERM"}, syscall.SIGTERM, false, ""},
		{[]string{"-s", "0"}, 0, false, ""},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil || !got.hasSignal || got.signal != tt.signal || got.exact != tt.exact || got.query != tt.query {
			t.Errorf("parseArgs(%q) = %+v, %v; want signal %v, exact %v, query %q", tt.args, got, err, tt.signal, tt.exact, tt.query)
		}
	}
	queries := []struct {
		args  []string
		query string
	}{
		{[]string{"--", "-s", "TERM"}, "-s TERM"},
		{[]string{"node", "-s", "TERM"}, "node -s TERM"},
		{[]string{"node", "--signal=TERM"}, "node --signal=TERM"},
	}
	for _, tt := range queries {
		got, err := parseArgs(tt.args)
		if err != nil || got.hasSignal || got.query != tt.query {
			t.Errorf("parseArgs(%q) = %+v, %v; want no signal and query %q", tt.args, got, err, tt.query)
		}
	}
	if got, err := parseArgs([]string{"-s", "TERM", "-h"}); err != nil || !got.help {
		t.Errorf("parseArgs(-s TERM -h) = %+v, %v; want help", got, err)
	}
}

func TestTimeoutOptionIsInTheUsage(t *testing.T) {
	_, out, _ := runKil(t, nil, "--help")
	if !strings.Contains(out, "--timeout DUR") {
		t.Errorf("kil --help = %q; want --timeout DUR listed", out)
	}
}

// An invalid --timeout, or one with signal 0, which ends no process, is
// rejected before the process list is read.
func TestInvalidTimeoutIsRejectedBeforeListing(t *testing.T) {
	tests := []struct {
		env  string
		args []string
		want string
	}{
		{"", []string{"--timeout"}, "kil: --timeout needs a duration"},
		{"", []string{"--timeout", "0"}, `kil: --timeout: invalid duration "0"`},
		{"", []string{"--timeout", "-1s"}, `kil: --timeout: invalid duration "-1s"`},
		{"", []string{"--timeout", "soon"}, `kil: --timeout: invalid duration "soon"`},
		{"", []string{"--timeout="}, `kil: --timeout: invalid duration ""`},
		{"", []string{"--timeout", "-5"}, `kil: --timeout: invalid duration "-5"`},
		{"", []string{"--timeout", "NaN"}, `kil: --timeout: invalid duration "NaN"`},
		{"", []string{"--timeout", "Inf"}, `kil: --timeout: invalid duration "Inf"`},
		{"", []string{"--timeout", "1e30"}, `kil: --timeout: invalid duration "1e30"`},
		{"", []string{"--timeout", "1e-12"}, `kil: --timeout: invalid duration "1e-12"`},
		{"", []string{"--timeout", "9999999h"}, `kil: --timeout: invalid duration "9999999h"`},
		{"", []string{"--timeout", "5s", "-s", "0"}, "kil: --timeout needs a signal that ends processes"},
		{"0", []string{"--timeout", "5s"}, "kil: --timeout needs a signal that ends processes"},
	}
	for _, tt := range tests {
		runs := fakePS(t)
		code, _, errOut := runKil(t, map[string]string{"KIL_SIGNAL": tt.env}, tt.args...)
		if code != exitError || !strings.HasPrefix(errOut, tt.want) {
			t.Errorf("KIL_SIGNAL=%s kil %q = %d, %q; want %d and an error starting %q", tt.env, tt.args, code, errOut, exitError, tt.want)
		}
		if _, err := os.Stat(runs); !os.IsNotExist(err) {
			t.Errorf("kil %q ran ps before rejecting the option, or its record is unreadable: %v", tt.args, err)
		}
	}
}

func TestTimeoutValues(t *testing.T) {
	tests := []struct {
		args  []string
		want  time.Duration
		exact bool
		query string
	}{
		{[]string{"--timeout", "5"}, 5 * time.Second, false, ""},
		{[]string{"--timeout", "1.5", "node"}, 1500 * time.Millisecond, false, "node"},
		{[]string{"-e", "--timeout", "500ms", "--", "-zsh"}, 500 * time.Millisecond, true, "-zsh"},
		{[]string{"--timeout=2m", "-s", "TERM", "node", "dev"}, 2 * time.Minute, false, "node dev"},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil || got.timeout != tt.want || got.exact != tt.exact || got.query != tt.query {
			t.Errorf("parseArgs(%q) = %+v, %v; want timeout %v, exact %v, query %q", tt.args, got, err, tt.want, tt.exact, tt.query)
		}
	}
	for _, args := range [][]string{{"node", "--timeout", "5"}, {"--", "--timeout", "5"}} {
		got, err := parseArgs(args)
		if err != nil || got.timeout != 0 || !strings.Contains(got.query, "--timeout 5") {
			t.Errorf("parseArgs(%q) = %+v, %v; want --timeout kept in the query", args, got, err)
		}
	}
}

// Arguments that are a colon and a port name ports to list the processes
// of; others, or any after --, stay in the query.
func TestPortArguments(t *testing.T) {
	tests := []struct {
		args  []string
		ports []int
		query string
	}{
		{[]string{":3000"}, []int{3000}, ""},
		{[]string{":3000", ":5173", "node"}, []int{3000, 5173}, "node"},
		{[]string{"node", ":3000", "dev"}, []int{3000}, "node dev"},
		{[]string{"-e", ":65535"}, []int{65535}, ""},
		{[]string{"--", ":3000"}, nil, ":3000"},
		{[]string{":abc", "node:3000"}, nil, ":abc node:3000"},
		{[]string{":"}, nil, ":"},
		{[]string{":1", ":03000", ":3000"}, []int{1, 3000}, ""},
		{[]string{":+3000", ":3000x"}, nil, ":+3000 :3000x"},
		{[]string{":3000", "--", ":5173"}, []int{3000}, ":5173"},
	}
	for _, tt := range tests {
		got, err := parseArgs(tt.args)
		if err != nil || !slices.Equal(got.ports, tt.ports) || got.query != tt.query {
			t.Errorf("parseArgs(%q) = %+v, %v; want ports %v and query %q", tt.args, got, err, tt.ports, tt.query)
		}
	}
}

func TestInvalidPortIsRejectedBeforeListing(t *testing.T) {
	for _, args := range [][]string{{":0"}, {":65536"}, {":99999999999999999999"}, {"node", ":3000", ":0"}} {
		arg := args[len(args)-1]
		runs := fakePS(t)
		code, _, errOut := runKil(t, nil, args...)
		want := fmt.Sprintf("kil: invalid port %q (use 1 to 65535, or 'kil -- %s' to search for it)", arg, arg)
		if code != exitError || !strings.HasPrefix(errOut, want) {
			t.Errorf("kil %s = %d, %q; want %d and %q", arg, code, errOut, exitError, want)
		}
		if _, err := os.Stat(runs); !os.IsNotExist(err) {
			t.Errorf("kil %s ran ps before rejecting the port: %v", arg, err)
		}
	}
}

func TestPortIsInTheUsage(t *testing.T) {
	_, out, _ := runKil(t, nil, "--help")
	if !strings.Contains(out, ":PORT") {
		t.Errorf("kil --help = %q; want :PORT described", out)
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
	reload := func() (ui.Listing, error) { return ui.Listing{}, errors.New("ps failed") }
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

// RUNEWIDTH_EASTASIAN=1 makes kil lay rows out with ambiguous-width
// characters two columns wide, before it lists anything.
func TestRunReadsAmbiguousWidthFirst(t *testing.T) {
	t.Cleanup(func() { width.SetAmbiguousWide(false) })
	env := map[string]string{"RUNEWIDTH_EASTASIAN": "1", "KIL_SIGNAL": "bogus"}
	var stdout, stderr bytes.Buffer
	if code := run(nil, func(k string) string { return env[k] }, &stdout, &stderr); code != exitError {
		t.Fatalf("run with an invalid KIL_SIGNAL = %d, want %d", code, exitError)
	}
	if width.Ambiguous() != 2 {
		t.Errorf("Ambiguous() = %d after run with RUNEWIDTH_EASTASIAN=1, want 2", width.Ambiguous())
	}
}

// A process with the PID and line of one kil signalled but another start
// time is a later process given the same PID, so it is reported as changed
// and not sent SIGKILL. A PID cannot be reused on demand, so ps is replaced.
func TestTimeoutTellsAReusedPIDByItsStartTime(t *testing.T) {
	sleeper := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$")
	sleeper.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	pid := sleeper.Process.Pid
	line := fmt.Sprintf("%d sleeper", pid)
	savedLines, savedStates := lookupLines, lookupStates
	defer func() { lookupLines, lookupStates = savedLines, savedStates }()
	lookupLines = func([]int) (map[int]string, error) { return map[int]string{pid: line}, nil }
	lookupStates = func([]int) (map[int]proc.State, error) {
		return map[int]proc.State{pid: {Start: "later"}}, nil
	}

	var stdout, stderr bytes.Buffer
	w := waited{Item: picker.Item{ID: line, Text: line}, pid: pid, start: "earlier"}
	code := waitAndKill([]waited{w}, 100*time.Millisecond, &stdout, &stderr, nil)
	if code != exitProblems || stderr.String() != "skipped: process changed, not sent SIGKILL: "+line+"\n" || stdout.String() != "" {
		t.Errorf("waitAndKill = %d, stdout %q, stderr %q; want the process reported as changed", code, stdout.String(), stderr.String())
	}
	if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the process was ended: %v", err)
	}
}

// A port held only by processes kil cannot see, as another user's without
// root, is not reported as free: kil names the user and suggests sudo. No
// such process can be made without root, so the lookup is replaced.
func TestPortHeldByAProcessKilCannotSee(t *testing.T) {
	saved := findPorts
	defer func() { findPorts = saved }()
	findPorts = func([]int) (port.Result, error) {
		return port.Result{PIDs: map[int]bool{}, Hidden: map[int][]int{3000: {0}}}, nil
	}
	code, _, errOut := runKil(t, nil, ":3000")
	want := "kil: port 3000 is held by a process kil cannot see, with a socket of user root; try sudo kil :3000\n"
	if code != exitProblems || errOut != want {
		t.Errorf("kil :3000 = %d, %q; want %d and %q", code, errOut, exitProblems, want)
	}
}

// When KIL_TEST_SLEEPER is set, this test is a process for kil to signal.
func TestSleeperChild(t *testing.T) {
	if os.Getenv("KIL_TEST_SLEEPER") == "" {
		t.Skip("run by the tests that signal it")
	}
	time.Sleep(time.Minute)
}

// When KIL_TEST_PORTS_CHILD is set, this test runs kil :3000 :5173 with the
// port lookup replaced as KIL_TEST_PORTS_CHILD names, and writes what kil
// reported to stderr: "hidden" gives a visible holder of 3000 and a hidden
// one of 5173; "late" gives a holder that starts after the process list
// was read, as a server starting as kil does.
func TestPortsChild(t *testing.T) {
	mode := os.Getenv("KIL_TEST_PORTS_CHILD")
	if mode == "" {
		t.Skip("run by the port tests")
	}
	switch mode {
	case "hidden":
		findPorts = func([]int) (port.Result, error) {
			return port.Result{PIDs: map[int]bool{os.Getppid(): true}, Hidden: map[int][]int{5173: {0}}}, nil
		}
	case "missing":
		// A PID that no process has, as one that exited after the lookup.
		findPorts = func([]int) (port.Result, error) {
			return port.Result{PIDs: map[int]bool{1<<22 + 7: true}}, nil
		}
	case "late":
		// The holder is started through a shell that exits at once, so
		// that it is not one of kil's descendants, which kil hides.
		late := 0
		findPorts = func([]int) (port.Result, error) {
			if late == 0 {
				sh := exec.Command("sh", "-c", `"$0" -test.run='^TestSleeperChild$' >/dev/null 2>&1 & echo $!`, os.Args[0])
				sh.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1", "KIL_TEST_PORTS_CHILD=")
				out, err := sh.Output()
				if err != nil {
					return port.Result{}, err
				}
				if late, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
					return port.Result{}, err
				}
				time.Sleep(100 * time.Millisecond)
			}
			return port.Result{PIDs: map[int]bool{late: true}}, nil
		}
		defer func() {
			if late > 0 {
				_ = syscall.Kill(late, syscall.SIGKILL)
			}
		}()
	}
	var stdout bytes.Buffer
	code := run([]string{":3000", ":5173"}, func(string) string { return "" }, &stdout, os.Stderr)
	fmt.Fprintf(os.Stderr, "code=%d\n", code)
}

// runPortsChild runs TestPortsChild in mode in a session of its own, which
// has no controlling terminal, so that kil cannot open the picker on the
// terminal go test may run on, and fails at it instead.
func runPortsChild(t *testing.T, mode string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPortsChild$")
	cmd.Env = append(os.Environ(), "KIL_TEST_PORTS_CHILD="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stderr.String()
}

// A port held only by an unseen process is reported even when another
// port given has processes kil lists, before the picker starts.
func TestHiddenHolderIsReportedBesideVisibleOnes(t *testing.T) {
	out := runPortsChild(t, "hidden")
	want := "kil: port 5173 is held by a process kil cannot see, with a socket of user root; try sudo kil :5173\n"
	if !strings.HasPrefix(out, want) || !strings.Contains(out, "no terminal") {
		t.Errorf("kil :3000 :5173 reported %q; want it to start with %q and go on to the picker", out, want)
	}
}

// A holder that is still missing from the list after kil read it again is
// not reported as no process: the port had one, which exited or started as
// kil read the list.
func TestPortHolderMissingFromTheList(t *testing.T) {
	out := runPortsChild(t, "missing")
	want := "kil: a process found on ports 3000, 5173 was not in the process list, as it started or exited meanwhile; try again\ncode=1\n"
	if out != want {
		t.Errorf("kil :3000 :5173 reported %q; want %q", out, want)
	}
}

// A holder that starts after the process list was read is not missed: kil
// reads the list again rather than report the port as free.
func TestPortHolderThatStartsAfterTheList(t *testing.T) {
	out := runPortsChild(t, "late")
	if strings.Contains(out, "no process found") || !strings.Contains(out, "no terminal") {
		t.Errorf("kil :3000 :5173 reported %q; want the late holder listed, so kil goes on to the picker", out)
	}
}

// A process whose start time could not be read before the signal cannot be
// told from a later process given its PID, so it is not sent SIGKILL, and
// the failure is reported. /proc and ps cannot be made to fail from
// outside, so the lookup is replaced.
func TestTimeoutWithoutAStartTime(t *testing.T) {
	sleeper := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$")
	sleeper.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	pid := sleeper.Process.Pid
	line := fmt.Sprintf("%d sleeper", pid)
	savedLines, savedStates := lookupLines, lookupStates
	defer func() { lookupLines, lookupStates = savedLines, savedStates }()
	lookupLines = func([]int) (map[int]string, error) { return map[int]string{pid: line}, nil }
	calls := 0
	lookupStates = func(pids []int) (map[int]proc.State, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("states unreadable")
		}
		return savedStates(pids)
	}

	b := &batches{timeout: 300 * time.Millisecond}
	var stdout, stderr bytes.Buffer
	// SIGCONT leaves the sleeper running, as one that ignored SIGTERM.
	if code := b.signal([]picker.Item{{ID: line, Text: line}}, syscall.SIGCONT, &stdout, &stderr, nil); code != exitOK {
		t.Fatalf("signal = %d, stderr %q", code, stderr.String())
	}
	code := waitAndKill(b.waiting, b.timeout, &stdout, &stderr, nil)
	want := "failed: reading the start time: states unreadable, not sent SIGKILL: " + line + "\n"
	if code != exitProblems || stderr.String() != want || strings.Contains(stdout.String(), "SIGKILL") {
		t.Errorf("waitAndKill = %d, stdout %q, stderr %q; want %d and %q", code, stdout.String(), stderr.String(), exitProblems, want)
	}
	if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the process was ended: %v", err)
	}
}

// The picker's note on a listing of ports says what it leaves out, as kil
// reports as it starts: ports held by processes kil cannot see, in the order
// given, with the users of their sockets; a process found on a port but
// missing from the list; and, where other users' processes are not found
// without root, an empty listing. Each part starts with what to do, so that
// a narrow terminal cuts the detail rather than that.
func TestPortNote(t *testing.T) {
	tests := []struct {
		name     string
		found    port.Result
		unlisted bool
		empty    bool
		want     string
	}{
		{"none", port.Result{Hidden: map[int][]int{}}, false, false, ""},
		{"one", port.Result{Hidden: map[int][]int{3000: {0}}}, false, false,
			"port 3000: try sudo; kil cannot see what holds it (socket of root)"},
		{"one port, two users", port.Result{Hidden: map[int][]int{3000: {0, 4242424}}}, false, false,
			"port 3000: try sudo; kil cannot see what holds it (sockets of root, uid 4242424)"},
		{"two ports, one user", port.Result{Hidden: map[int][]int{3000: {0}, 5173: {0}}}, false, false,
			"ports 5173, 3000: try sudo; kil cannot see what holds them (sockets of root)"},
		{"several", port.Result{Hidden: map[int][]int{5173: {0, 4242424}, 3000: {4242424}}}, false, false,
			"ports 5173, 3000: try sudo; kil cannot see what holds them (sockets of root, uid 4242424)"},
		{"unlisted", port.Result{Hidden: map[int][]int{}}, true, false,
			"ports 5173, 3000, 8080: Ctrl-R again; a holder was not in the process list"},
		{"unlisted and hidden", port.Result{Hidden: map[int][]int{3000: {0}}}, true, false,
			"port 3000: try sudo; kil cannot see what holds it (socket of root); ports 5173, 3000, 8080: Ctrl-R again; a holder was not in the process list"},
		{"may miss, empty", port.Result{MayMiss: true}, false, true,
			"ports 5173, 3000, 8080: none found; try sudo for other users' processes"},
		{"may miss, some listed", port.Result{MayMiss: true}, false, false, ""},
		{"may not miss, empty", port.Result{Hidden: map[int][]int{}}, false, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portNote([]int{5173, 3000, 8080}, tt.found, tt.unlisted, tt.empty)
			if got != tt.want {
				t.Errorf("portNote = %q, want %q", got, tt.want)
			}
		})
	}
}

// What to do fits a terminal of 80 columns beside the info line's counts and
// [exact], with a 5-digit port and a long user name.
func TestPortNoteActionFitsEightyColumns(t *testing.T) {
	notes := map[string]string{
		"hidden":   portNote([]int{50000}, port.Result{Hidden: map[int][]int{50000: {4242424}}}, false, false),
		"unlisted": portNote([]int{50000, 50001}, port.Result{}, true, false),
		"may miss": portNote([]int{50000, 50001}, port.Result{MayMiss: true}, false, true),
	}
	before := len("  123/456 (78) [exact] ")
	for name, note := range notes {
		for _, action := range []string{"try sudo", "Ctrl-R again"} {
			if i := strings.Index(note, action); i >= 0 && before+i+len(action) > 80 {
				t.Errorf("%s note %q puts %q past column 80", name, note, action)
			}
		}
	}
}
