// Command kil filters processes interactively and signals the selected ones.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/gdamore/tcell/v2"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/signal"
	"github.com/oooooooo/kil/internal/ui"
	"github.com/oooooooo/kil/internal/width"
)

const usage = `Usage: kil [--] [query...]

Filter processes interactively and send a signal to the selected ones.
All arguments are joined with spaces into the initial query.

Options:
  -e, --exact  Match plain terms exactly, as fzf --exact ('term matches fuzzily)
  -h, --help   Show this help and exit
  --version    Show the version and exit
  --           Treat the remaining arguments as the query (e.g. kil -- -zsh)

Environment:
  KIL_SIGNAL   Signal number or name to send (default: 9, i.e. SIGKILL)
  KIL_EXACT    Set to 1 to start in exact mode, as with -e

Exit status:
  0      every selected process was signalled
  1      nothing was selected, or some processes were skipped or failed
  2      usage error, invalid KIL_SIGNAL or KIL_EXACT, listing processes
         failed, or no terminal
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
	help    bool
	version bool
	exact   bool
	query   string
}

func parseArgs(args []string) (options, error) {
	var opts options
	for i, a := range args {
		switch {
		case a == "-h" || a == "--help":
			return options{help: true}, nil
		case a == "--version":
			return options{version: true}, nil
		case a == "-e" || a == "--exact":
			opts.exact = true
		case a == "--":
			opts.query = strings.Join(args[i+1:], " ")
			return opts, nil
		case strings.HasPrefix(a, "-") && a != "-":
			return options{}, fmt.Errorf("unknown option: %s (use 'kil -- %s' to search for it)", a, a)
		default:
			opts.query = strings.Join(args[i:], " ")
			return opts, nil
		}
	}
	return opts, nil
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
	sig, err := signal.Parse(getenv("KIL_SIGNAL"))
	if err != nil {
		reportf(stderr, "kil: invalid KIL_SIGNAL: %v (use a number like 15 or a name like TERM)\n", err)
		return exitError
	}

	list := func() ([]picker.Item, error) {
		procs, err := proc.List(os.Getpid())
		if err != nil {
			return nil, err
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

	// Until now, signals take their default action: nothing has been
	// signalled and the terminal is as it was, and a ps that hangs cannot
	// keep kil from ending.
	in := watchSignals()
	defer in.close()

	sent := &batches{}
	model, err := pick(ui.New(items, opts.query, signal.Label(sig), list).WithExact(opts.exact || envExact).WithSender(sender(sent, sig, in)), in)
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
		// Like Ctrl-C, a signal ends kil without signalling the rest.
		return code
	}
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

	code := signalAll(model.Targets(), sig, stdout, stderr, in)
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
