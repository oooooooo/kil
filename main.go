// Command kil filters processes interactively and signals the selected ones.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	ossignal "os/signal"
	"os/user"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/port"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/signal"
	"github.com/oooooooo/kil/internal/ui"
	"github.com/oooooooo/kil/internal/width"
)

const usage = `Usage: kil [options] [:PORT...] [--] [query...]

Filter processes interactively and send a signal to the selected ones.
Options come first: the arguments from the first that is not an option
are joined with spaces into the initial query. An argument :PORT, as :3000,
lists only the processes listening on that TCP port or bound to that UDP
port; after --, it is part of the query.

Options:
  -e, --exact       Match plain terms exactly, as fzf --exact ('term matches
                    fuzzily)
  -s, --signal SIG  Signal to send, as 15 or TERM; overrides KIL_SIGNAL
  --timeout DUR     Wait up to DUR, as 5s or 5, for the processes to end and
                    send SIGKILL to the rest; the signal is SIGTERM unless
                    -s or KIL_SIGNAL chooses another
  -h, --help        Show this help and exit
  --version         Show the version and exit
  --                Treat the remaining arguments as the query, as kil -- -zsh

Environment:
  KIL_SIGNAL   Signal number or name to send (default: 9, i.e. SIGKILL)
  KIL_EXACT    Set to 1 to start in exact mode, as with -e

Exit status:
  0      every selected process was signalled
  1      nothing was selected, or some processes were skipped or failed
  2      usage error, invalid -s, --timeout, KIL_SIGNAL, or KIL_EXACT,
         listing processes failed, or no terminal
  130    aborted with Ctrl-C / Esc
  128+n  ended by signal n: 129 for SIGHUP, 131 for SIGQUIT, 143 for SIGTERM
`

const (
	exitOK       = 0
	exitProblems = 1
	exitError    = 2
	exitAborted  = 130
)

func main() {
	ignoreBrokenPipe()
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// ignoreBrokenPipe makes a write to a closed stdout, as in `kil | head -n 0`,
// fail instead of ending kil with SIGPIPE before every selected process is
// signalled. The report helpers ignore that failure.
func ignoreBrokenPipe() {
	ossignal.Ignore(syscall.SIGPIPE)
}

type options struct {
	help      bool
	version   bool
	exact     bool
	signal    syscall.Signal
	hasSignal bool
	timeout   time.Duration
	ports     []int
	query     string
}

func parseArgs(args []string) (options, error) {
	var opts options
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return options{help: true}, nil
		case a == "--version":
			return options{version: true}, nil
		case a == "-e" || a == "--exact":
			opts.exact = true
		case a == "-s" || a == "--signal":
			if i+1 == len(args) {
				return options{}, fmt.Errorf("%s needs a signal, a number like 15 or a name like TERM", a)
			}
			i++
			if err := opts.setSignal(a, args[i]); err != nil {
				return options{}, err
			}
		case a == "--timeout":
			if i+1 == len(args) {
				return options{}, fmt.Errorf("--timeout needs a duration, as 5s or 5")
			}
			i++
			if err := opts.setTimeout(args[i]); err != nil {
				return options{}, err
			}
		case strings.HasPrefix(a, "--timeout="):
			if err := opts.setTimeout(strings.TrimPrefix(a, "--timeout=")); err != nil {
				return options{}, err
			}
		case strings.HasPrefix(a, "--signal="):
			if err := opts.setSignal("--signal", strings.TrimPrefix(a, "--signal=")); err != nil {
				return options{}, err
			}
		case a == "--":
			opts.query = strings.Join(args[i+1:], " ")
			return opts, nil
		case strings.HasPrefix(a, "-") && a != "-":
			return options{}, fmt.Errorf("unknown option: %s (use 'kil -- %s' to search for it)", a, a)
		default:
			return opts, opts.setQuery(args[i:])
		}
	}
	return opts, nil
}

// portArg matches an argument that names a port.
var portArg = regexp.MustCompile(`^:[0-9]+$`)

// setQuery sets the query from args, taking out the ports they name up to
// a --, after which every argument is part of the query.
func (o *options) setQuery(args []string) error {
	var words []string
	for i, a := range args {
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if !portArg.MatchString(a) {
			words = append(words, a)
			continue
		}
		port, err := strconv.Atoi(a[1:])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid port %q (use 1 to 65535, or 'kil -- %s' to search for it)", a, a)
		}
		if !slices.Contains(o.ports, port) {
			o.ports = append(o.ports, port)
		}
	}
	o.query = strings.Join(words, " ")
	return nil
}

// setSignal sets the signal given to the option name. Every value is
// checked, even one a later option replaces, so that a typo is reported.
func (o *options) setSignal(name, value string) error {
	// signal.Parse takes an empty value as the default, which suits an
	// unset KIL_SIGNAL but would hide an empty -s.
	sig, err := signal.Parse(value)
	if value == "" || err != nil {
		return fmt.Errorf("%s: invalid signal %q (use a number like 15 or a name like TERM)", name, value)
	}
	o.signal, o.hasSignal = sig, true
	return nil
}

// setTimeout sets the timeout from value: a duration, as 5s or 500ms, or a
// number of seconds, as timeout(1) takes.
func (o *options) setTimeout(value string) error {
	d, err := time.ParseDuration(value)
	if secs, ferr := strconv.ParseFloat(value, 64); ferr == nil {
		err = nil
		ns := secs * float64(time.Second)
		if math.IsNaN(ns) || ns >= math.MaxInt64 {
			err = errors.New("out of range")
		}
		d = time.Duration(ns)
	}
	if err != nil || d <= 0 {
		return fmt.Errorf("--timeout: invalid duration %q (use a duration as 5s or 500ms, or seconds as 5)", value)
	}
	o.timeout = d
	return nil
}

// exactFromEnv parses KIL_EXACT. An unset or empty value means fuzzy.
func exactFromEnv(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	exact, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid KIL_EXACT: %q (use 1 or 0)", v)
	}
	return exact, nil
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		reportf(stderr, "kil: %v\n%s", err, usage)
		return exitError
	}
	if opts.version {
		// As the usage, the version is the whole output, so failing to
		// write it is an error.
		if _, err := fmt.Fprintf(stdout, "kil %s\n", version()); err != nil {
			reportf(stderr, "kil: write error: %v\n", err)
			return exitError
		}
		return exitOK
	}
	if opts.help {
		// Unlike the reports of signals sent, the usage is the whole
		// output, so failing to write it is an error, as in GNU tools.
		if _, err := fmt.Fprint(stdout, usage); err != nil {
			reportf(stderr, "kil: write error: %v\n", err)
			return exitError
		}
		return exitOK
	}
	envExact, err := exactFromEnv(getenv("KIL_EXACT"))
	if err != nil {
		reportf(stderr, "kil: %v\n", err)
		return exitError
	}
	// Rows are laid out with this width from the listing on, so it is set
	// first.
	width.SetAmbiguousWide(width.FromEnv(getenv))
	sig := opts.signal
	if !opts.hasSignal {
		env := getenv("KIL_SIGNAL")
		sig, err = signal.Parse(env)
		if err != nil {
			reportf(stderr, "kil: invalid KIL_SIGNAL: %v (use a number like 15 or a name like TERM)\n", err)
			return exitError
		}
		// SIGKILL leaves nothing to wait for, so a timeout asks for the
		// signal a process can end on cleanly.
		if env == "" && opts.timeout > 0 {
			sig = syscall.SIGTERM
		}
	}
	if sig == 0 && opts.timeout > 0 {
		reportf(stderr, "kil: --timeout needs a signal that ends processes; signal 0 only checks them\n")
		return exitError
	}
	label := signal.Label(sig)
	if opts.timeout > 0 {
		label += fmt.Sprintf(" (KILL after %v)", opts.timeout)
	}

	var found port.Result
	list := func() ([]picker.Item, error) {
		procs, err := proc.List(os.Getpid())
		if err != nil {
			return nil, err
		}
		if len(opts.ports) > 0 {
			// The ports are read after the listing, so that a process that
			// took a port meanwhile is not missing from it.
			if found, err = findPorts(opts.ports); err != nil {
				return nil, fmt.Errorf("finding the processes on the ports: %w", err)
			}
			procs = slices.DeleteFunc(procs, func(p proc.Process) bool { return !found.PIDs[p.PID] })
		}
		items := make([]picker.Item, len(procs))
		for i, p := range procs {
			items[i] = picker.Item{ID: p.Identity, Text: p.Raw, Frozen: p.Frozen}
		}
		return items, nil
	}
	items, err := list()
	if err != nil {
		reportf(stderr, "kil: failed to list processes: %v; nothing was signalled\n", err)
		return exitError
	}
	if len(opts.ports) > 0 {
		hiddenOnPorts(opts.ports, found, stderr)
		if len(items) == 0 {
			// There is nothing to pick from, so the terminal is left alone.
			nothingOnPorts(opts.ports, found, stderr)
			return exitProblems
		}
	}

	// Until now, signals take their default action: nothing has been
	// signalled and the terminal is as it was, and a ps that hangs cannot
	// keep kil from ending.
	in := watchSignals()
	defer in.close()

	sent := &batches{timeout: opts.timeout}
	model, err := pick(ui.New(items, opts.query, label, list).WithExact(opts.exact || envExact).WithSender(sender(sent, sig, in)), in)
	if err != nil {
		return pickFailed(sent, err, stdout, stderr, in)
	}
	return finish(model, sig, sent, stdout, stderr, in)
}

// releaseVersion is set at release builds with -ldflags "-X
// main.releaseVersion=v1.2.3".
var releaseVersion string

// version returns kil's version: the release's, or the one Go records in
// the build, a module version for go install, or a pseudo-version with the
// commit, marked "+dirty" for a tree with changes, for a local build.
func version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}

// pickFailed reports a picker failure after the reports of the rows already
// signalled, which would otherwise be lost. A signal caught meanwhile, as
// when the terminal hangs up, still gives kil its status.
func pickFailed(sent *batches, err error, stdout, stderr io.Writer, in *interrupts) int {
	sent.flush(stdout, stderr)
	notKilled(sent.waiting, stderr)
	reportf(stderr, "kil: %v\n", err)
	if code, ok := in.ended(); ok {
		return code
	}
	return exitError
}

// batches holds the reports of rows signalled before the picker ended,
// which cannot be printed while the picker owns the screen.
type batches struct {
	// reports keeps stdout and stderr reports in one sequence, so that they
	// come out in the order the processes were handled when both go to the
	// same file.
	reports []heldReport
	code    int
	// sent counts the rows handed to signalAll, to tell whether anything
	// may have been signalled.
	sent int
	// timeout is how long kil waits for the processes signalled, kept in
	// waiting, before it sends SIGKILL; 0 means it does not wait.
	timeout time.Duration
	waiting []waited
}

// signal signals targets, as signalAll does, and keeps those signalled for
// the wait. A stopped process acts on no signal but SIGKILL until it is
// continued, so with a timeout each is sent SIGCONT after the signal,
// as systemd does, unless the signal chosen stops or continues it.
func (b *batches) signal(targets []picker.Item, sig syscall.Signal, stdout, stderr io.Writer, in *interrupts) int {
	var sent []picker.Item
	code := signalTargets(targets, sig, stdout, stderr, in, &sent)
	if b.timeout > 0 && len(sent) > 0 {
		if !stopsOrContinues(sig) {
			continueAll(sent, in)
		}
		b.waiting = append(b.waiting, started(sent)...)
	}
	return code
}

func stopsOrContinues(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGCONT, syscall.SIGKILL:
		return true
	}
	return false
}

// continueAll sends SIGCONT to targets through the same check as any
// signal. A failure only leaves a stopped process to SIGKILL, so it is not
// reported.
func continueAll(targets []picker.Item, in *interrupts) {
	signal.SendAll(toSend(targets), syscall.SIGCONT, lookupLines, func() bool { return in.stopping() != nil }, func(int, signal.Outcome, error) {})
}

// toSend returns the targets that have a PID, as signal.SendAll takes them.
func toSend(targets []picker.Item) []signal.Target {
	var send []signal.Target
	for _, t := range targets {
		var pid int
		if _, err := fmt.Sscan(t.ID, &pid); err == nil {
			send = append(send, signal.Target{PID: pid, Want: t.ID})
		}
	}
	return send
}

type heldReport struct {
	toStderr bool
	text     []byte
}

// heldWriter adds what is written to it to b's reports.
type heldWriter struct {
	b        *batches
	toStderr bool
}

func (w heldWriter) Write(p []byte) (int, error) {
	w.b.reports = append(w.b.reports, heldReport{w.toStderr, bytes.Clone(p)})
	return len(p), nil
}

func (b *batches) stdout() io.Writer { return heldWriter{b, false} }
func (b *batches) stderr() io.Writer { return heldWriter{b, true} }

// flush prints the held reports in order.
func (b *batches) flush(stdout, stderr io.Writer) {
	for _, r := range b.reports {
		w := stdout
		if r.toStderr {
			w = stderr
		}
		_, _ = w.Write(r.text)
	}
	b.reports = nil
}

// signalled says what a failure leaves signalled: after a partial Enter,
// "nothing" would hide the processes reported above it.
func (b *batches) signalled() string {
	if b.sent > 0 {
		return "only the processes reported above were signalled"
	}
	return "nothing was signalled"
}

// finish reports how the picker ended and signals what it accepted, unless
// a signal was caught first.
func finish(model ui.Model, sig syscall.Signal, sent *batches, stdout, stderr io.Writer, in *interrupts) int {
	sent.flush(stdout, stderr)
	if code, ok := in.ended(); ok {
		// Like Ctrl-C, a signal ends kil without signalling the rest, or
		// waiting for those signalled.
		notKilled(sent.waiting, stderr)
		return code
	}
	code := finishPicked(model, sig, sent, stdout, stderr, in)
	if s := in.stopping(); s != nil {
		notKilled(sent.waiting, stderr)
		return exitSignalled(s)
	}
	if len(sent.waiting) == 0 {
		return code
	}
	// The processes signalled before the picker ended, by a partial Enter,
	// were chosen as much as the rest, so they are waited for whether
	// the picker ended with Enter, Esc, or Ctrl-C.
	return max(code, waitAndKill(sent.waiting, sent.timeout, stdout, stderr, in))
}

// notKilled reports the processes signalled that kil leaves without
// waiting for them or sending SIGKILL.
func notKilled(signalled []waited, stderr io.Writer) {
	for _, w := range signalled {
		reportf(stderr, "skipped: not sent SIGKILL: %s\n", w.Text)
	}
}

func finishPicked(model ui.Model, sig syscall.Signal, sent *batches, stdout, stderr io.Writer, in *interrupts) int {
	switch model.Outcome() {
	case ui.Aborted:
		// The user chose to leave, so there is nothing more to report.
		return exitAborted
	case ui.Stopped:
		for _, it := range model.Left() {
			reportf(stderr, "skipped: not signalled: %s\n", it.Text)
		}
		return exitProblems
	case ui.NothingSelected:
		reportln(stderr, "kil: nothing selected")
		return exitProblems
	case ui.ListFailed:
		for _, it := range model.Left() {
			reportf(stderr, "skipped: not signalled: %s\n", it.Text)
		}
		reportf(stderr, "kil: failed to list processes: %v; %s\n", model.Err(), sent.signalled())
		return exitError
	case ui.Accepted:
	default:
		reportf(stderr, "kil: picker ended unexpectedly; %s\n", sent.signalled())
		return exitError
	}

	code := sent.signal(model.Targets(), sig, stdout, stderr, in)
	if in.stopping() != nil {
		return code
	}
	return max(sent.code, code)
}

// pick runs the picker on the controlling terminal, so that stdout and
// stderr carry only the results and can be redirected, as with fzf. A
// signal caught ends the picker.
func pick(m ui.Model, in *interrupts) (ui.Model, error) {
	s, err := newScreen(os.Getenv("TERM"))
	if err != nil {
		return m, fmt.Errorf("no terminal: %w", err)
	}
	if err := s.Init(); err != nil {
		return m, fmt.Errorf("no terminal: %w", err)
	}
	defer s.Fini()

	// SIGTSTP from outside suspends kil as Ctrl-Z does. Left to its default
	// action, it stopped kil with the terminal in raw mode.
	tstp := make(chan os.Signal, 1)
	ossignal.Notify(tstp, syscall.SIGTSTP)
	defer ossignal.Stop(tstp)

	ext := make(chan ui.Msg)
	ended := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-in.whenCaught():
			select {
			case ext <- ui.QuitMsg{}:
			case <-ended:
			}
		case <-ended:
		}
	})
	wg.Go(func() {
		for {
			select {
			case <-tstp:
				select {
				case ext <- ui.SuspendMsg{}:
				case <-ended:
					return
				}
			case <-ended:
				return
			}
		}
	})
	final, err := ui.Run(s, m, ui.Control{
		External:   ext,
		CanSuspend: canSuspend(in, jobControlled),
		Stop:       stopJob,
	})
	close(ended)
	wg.Wait()
	if err != nil {
		return final, fmt.Errorf("picker failed: %w", err)
	}
	return final, nil
}

// newScreen returns a screen for the terminal term names. Without a term
// that tcell knows, or with dumb, which cannot move the cursor, the screen is
// xterm's, which most terminals follow, as kil drew before it used tcell.
// Without TERM, or with dumb, the terminal may not show colors, so the
// screen has none.
func newScreen(term string) (tcell.Screen, error) {
	ti, err := tcell.LookupTerminfo(term)
	if err != nil || term == "dumb" {
		if ti, err = tcell.LookupTerminfo("xterm-256color"); err != nil {
			return nil, err
		}
	}
	s, err := tcell.NewTerminfoScreenFromTtyTerminfo(nil, ti)
	if err != nil {
		return nil, err
	}
	if term == "" || term == "dumb" {
		return monochrome{s}, nil
	}
	return s, nil
}

// monochrome is a screen drawn without colors.
type monochrome struct{ tcell.Screen }

func (monochrome) Colors() int { return 0 }

// signalAll signals targets and reports each result. SIGINT, SIGTERM, or
// SIGQUIT stops it, as it would kill(1): the targets not signalled are
// reported as skipped, and it returns the status for that signal. SIGHUP does
// not stop it: the terminal hangs up when kil kills the shell it was started
// from, which must not leave the rest of the selection unsignalled.
func signalAll(targets []picker.Item, sig syscall.Signal, stdout, stderr io.Writer, in *interrupts) int {
	return signalTargets(targets, sig, stdout, stderr, in, nil)
}

// signalTargets is signalAll that also adds the targets signalled to sent,
// if not nil.
func signalTargets(targets []picker.Item, sig syscall.Signal, stdout, stderr io.Writer, in *interrupts, sent *[]picker.Item) int {
	stopped := func() bool { return in.stopping() != nil }
	code := exitOK
	// The processes are signalled together; at[i] is the position in
	// targets of the i-th one, so that reports keep the order of targets.
	var send []signal.Target
	var at []int
	for i, t := range targets {
		var pid int
		if _, err := fmt.Sscan(t.ID, &pid); err == nil {
			send = append(send, signal.Target{PID: pid, Want: t.ID})
			at = append(at, i)
		}
	}
	next := 0
	// reportUpTo reports the targets before end that have no PID.
	reportUpTo := func(end int) {
		for ; next < end; next++ {
			reportf(stderr, "skipped: not a PID: %s\n", targets[next].Text)
			code = exitProblems
		}
	}
	signal.SendAll(send, sig, lookupLines, stopped, func(i int, outcome signal.Outcome, err error) {
		if outcome == signal.Cancelled {
			return
		}
		reportUpTo(at[i])
		t := targets[at[i]]
		next = at[i] + 1
		switch outcome {
		case signal.Sent:
			reportf(stdout, "sent %s: %s\n", signal.Label(sig), t.Text)
			if sent != nil {
				*sent = append(*sent, t)
			}
		case signal.Skipped:
			reportf(stderr, "skipped: process changed or exited: %s\n", t.Text)
			code = exitProblems
		default:
			// The reason comes first, as in "skipped: process changed or
			// exited: ...", so that the line is always last.
			reportf(stderr, "failed: %v: %s\n", err, t.Text)
			code = exitProblems
		}
	})
	// A stop leaves the targets from the first not reported unsignalled.
	if stopped() && next < len(targets) {
		return skipRest(targets[next:], stderr, in)
	}
	reportUpTo(len(targets))
	// A signal that came as the last process was signalled still ends kil
	// with its status.
	if s := in.stopping(); s != nil {
		return exitSignalled(s)
	}
	return code
}

// skipRest reports targets as not signalled after a signal stopped kil, and
// returns the status for that signal.
func skipRest(targets []picker.Item, stderr io.Writer, in *interrupts) int {
	for _, t := range targets {
		reportf(stderr, "skipped: not signalled: %s\n", t.Text)
	}
	return exitSignalled(in.stopping())
}

// The report helpers ignore write errors: the exit status already tells the
// caller whether every process was signalled, even if stdout or stderr is
// closed.

func reportf(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func reportln(w io.Writer, s string) { _, _ = fmt.Fprintln(w, s) }

// A waited is a process signalled with --timeout, with its start time, so
// that a process that later has its PID and command line is not taken for
// it. Start is "" when ps could not tell it, and gone for a process that had
// already exited.
type waited struct {
	picker.Item
	pid   int
	start string
}

const gone = "gone"

// started returns targets, which were all signalled and so have PIDs, with
// their start times. The times are read right after the signal: before it,
// they took another ps run for every partial Enter, and a PID reused in the
// moment between is as unlikely as one reused before the signal's check.
func started(targets []picker.Item) []waited {
	ws := make([]waited, len(targets))
	pids := make([]int, len(targets))
	for i, t := range targets {
		var pid int
		_, _ = fmt.Sscan(t.ID, &pid)
		ws[i], pids[i] = waited{Item: t, pid: pid, start: gone}, pid
	}
	states, err := lookupStates(pids)
	for i := range ws {
		switch st, ok := states[ws[i].pid]; {
		case err != nil:
			ws[i].start = ""
		case ok:
			ws[i].start = st.Start
		}
	}
	return ws
}

// lookupStates returns processes' states. Tests replace it.
var lookupStates = proc.States

// pollEvery is how often kil checks the processes it waits for.
const pollEvery = 250 * time.Millisecond

// waitAndKill waits up to timeout for targets to end, reporting each as it
// ends, then sends SIGKILL to the rest. A zombie has ended, although ps
// still lists it. A process whose line or start time changed in another way
// may have exited and its PID been reused, or rewritten its command line,
// so it is left alone rather than taken as exited or sent SIGKILL. SIGINT,
// SIGTERM, or SIGQUIT stops the wait, as it would kill(1); SIGHUP does not.
func waitAndKill(targets []waited, timeout time.Duration, stdout, stderr io.Writer, in *interrupts) int {
	code := exitOK
	deadline := time.Now().Add(timeout)
	pending := targets
	for {
		if s := in.stopping(); s != nil {
			notKilled(pending, stderr)
			return exitSignalled(s)
		}
		left, err := sortOut(pending, stdout, stderr, &code)
		if err != nil {
			for _, w := range pending {
				reportf(stderr, "failed: checking the process: %v: %s\n", err, w.Text)
			}
			return exitProblems
		}
		pending = left
		if len(pending) == 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(min(pollEvery, time.Until(deadline)))
	}
	if s := in.stopping(); s != nil {
		notKilled(pending, stderr)
		return exitSignalled(s)
	}
	if len(pending) == 0 {
		return code
	}
	return max(code, killRest(pending, timeout, stdout, stderr, in))
}

// lookupWaited returns the identities of the processes with the given PIDs
// with their start times, as waited.want gives them.
func lookupWaited(pids []int) (map[int]string, error) {
	lines, err := lookupLines(pids)
	if err != nil {
		return nil, err
	}
	states, err := lookupStates(pids)
	if err != nil {
		return nil, err
	}
	found := map[int]string{}
	for pid, line := range lines {
		if st, ok := states[pid]; ok && !st.Zombie {
			found[pid] = line + "\x00" + st.Start
		}
	}
	return found, nil
}

// want returns the identity lookupWaited gives the process while it is the
// one signalled.
func (w waited) want(now map[int]proc.State) string {
	start := w.start
	if start == "" {
		// ps could not tell the start time after the signal, so only the
		// line is compared.
		start = now[w.pid].Start
	}
	return w.ID + "\x00" + start
}

// sortOut reports the targets that exited or changed and returns those still
// running as they were chosen.
func sortOut(targets []waited, stdout, stderr io.Writer, code *int) ([]waited, error) {
	pids := make([]int, len(targets))
	for i, w := range targets {
		pids[i] = w.pid
	}
	lines, err := lookupLines(pids)
	if err != nil {
		return nil, err
	}
	states, err := lookupStates(pids)
	if err != nil {
		return nil, err
	}
	var left []waited
	for _, w := range targets {
		line, ok := lines[w.pid]
		st, stOK := states[w.pid]
		switch {
		case !ok || !stOK || st.Zombie:
			reportf(stdout, "exited: %s\n", w.Text)
		case w.start == gone || line+"\x00"+st.Start != w.want(states):
			reportf(stderr, "skipped: process changed, not sent SIGKILL: %s\n", w.Text)
			*code = exitProblems
		default:
			left = append(left, w)
		}
	}
	return left, nil
}

// killRest sends SIGKILL to targets once the timeout has passed, through
// the same check as the first signal, with the start time.
func killRest(targets []waited, timeout time.Duration, stdout, stderr io.Writer, in *interrupts) int {
	code := exitOK
	pids := make([]int, len(targets))
	for i, w := range targets {
		pids[i] = w.pid
	}
	// The start times are needed only for a process whose start time was
	// not known; without them, its check fails and it is not signalled.
	states, _ := lookupStates(pids)
	send := make([]signal.Target, len(targets))
	for i, w := range targets {
		send[i] = signal.Target{PID: w.pid, Want: w.want(states)}
	}
	var skipped []waited
	next := 0
	signal.SendAll(send, syscall.SIGKILL, lookupWaited, func() bool { return in.stopping() != nil }, func(i int, outcome signal.Outcome, err error) {
		if outcome == signal.Cancelled {
			return
		}
		next = i + 1
		w := targets[i]
		switch outcome {
		case signal.Sent:
			reportf(stdout, "sent SIGKILL after %v: %s\n", timeout, w.Text)
		case signal.Skipped:
			skipped = append(skipped, w)
		default:
			reportf(stderr, "failed: %v: %s\n", err, w.Text)
			code = exitProblems
		}
	})
	if s := in.stopping(); s != nil {
		notKilled(append(skipped, targets[next:]...), stderr)
		return exitSignalled(s)
	}
	if len(skipped) == 0 {
		return code
	}
	// A process skipped here exited or changed since the last check; one
	// that looks as it did again changed and changed back.
	left, err := sortOut(skipped, stdout, stderr, &code)
	if err != nil {
		for _, w := range skipped {
			reportf(stderr, "failed: checking the process: %v: %s\n", err, w.Text)
		}
		return exitProblems
	}
	for _, w := range left {
		reportf(stderr, "skipped: process changed, not sent SIGKILL: %s\n", w.Text)
		code = exitProblems
	}
	return code
}

// hiddenOnPorts reports the ports held by processes kil cannot see, with
// their users, so that such a port is not taken for a free one.
func hiddenOnPorts(ports []int, found port.Result, stderr io.Writer) {
	for _, p := range ports {
		uids := found.Hidden[p]
		if len(uids) == 0 {
			continue
		}
		names := make([]string, len(uids))
		for i, uid := range uids {
			names[i] = "uid " + strconv.Itoa(uid)
			if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
				names[i] = u.Username
			}
		}
		who := "user " + names[0]
		if len(names) > 1 {
			who = "users " + strings.Join(names, ", ")
		}
		reportf(stderr, "kil: port %d is held by a process of %s, which kil cannot see; try sudo kil :%d\n", p, who, p)
	}
}

// nothingOnPorts reports the ports on which nothing at all was found.
func nothingOnPorts(ports []int, found port.Result, stderr io.Writer) {
	var free []string
	for _, p := range ports {
		if len(found.Hidden[p]) == 0 {
			free = append(free, strconv.Itoa(p))
		}
	}
	if len(free) == 0 {
		return
	}
	which := "port " + free[0]
	if len(free) > 1 {
		which = "ports " + strings.Join(free, ", ")
	}
	if found.MayMiss {
		reportf(stderr, "kil: no process found on %s; another user's process is found only with sudo\n", which)
		return
	}
	reportf(stderr, "kil: no process found on %s\n", which)
}
