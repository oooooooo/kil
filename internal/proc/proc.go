// Package proc lists processes with ps, which unlike /proc works the same on
// Linux and macOS.
package proc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// listColumns is the column list for the listing ps run. The command name
// comes from a run of its own, nameColumns, where it is the last column: it
// can contain spaces, as "tmux: server" or a macOS path such as
// "/Applications/Visual Studio Code.app/...", so in one run with the command
// line after it, no rule found where it ended. The user name has no spaces.
// procps cuts it to 8 characters, as "systemd+", unless the column is
// widened, which ps on macOS and FreeBSD, which do not cut it, do not
// accept.
func listColumns() string {
	if runtime.GOOS == "linux" {
		return "pid=,ppid=,user:64=,args="
	}
	return "pid=,ppid=,user=,args="
}

// nameColumns is the column list for the ps run that reads command names.
const nameColumns = "pid=,ppid=,comm="

// psWide makes ps print whole lines on Linux and macOS: without it, ps cuts
// them to COLUMNS when that is set, hiding the end of long command lines from
// matching and from the check before signalling.
const psWide = "-ww"

// A Process is a process as kil lists it.
type Process struct {
	PID  int
	PPID int
	User string
	// Comm is the command name, as ps prints it.
	Comm string
	// Args is the command line.
	Args string
	// Identity is the process without the PPID, so that the same process
	// listed by two ps runs compares equal. The PPID is left out because
	// killing a parent reparents its children: a child selected with its
	// parent is still the same process and must not be skipped as changed.
	Identity string
	// Raw is the line kil shows and matches against, with the columns
	// padded to line up, as ps would.
	Raw string
	// Frozen is the number of runes of Raw before the command line: the
	// PID, PPID, USER, and COMM columns, which stay on screen when a long
	// line is scrolled.
	Frozen int
}

// widths measures text as kil's screen does.
var widths = &runewidth.Condition{EastAsianWidth: false}

// Parse joins the output of the listing ps run, with listColumns, and of
// the name ps run, with nameColumns, by PID, and lays the columns out. A
// process missing from one run exited or started between them and is left
// out. The PPID may differ between the runs, as killing a parent reparents
// its children, so it is not compared. A PID reused between the runs gives
// a line that matches neither process, which the check before signalling
// skips.
func Parse(listing, names string) ([]Process, error) {
	byPID := map[int]string{}
	for line := range strings.Lines(names) {
		pid, _, comm, err := splitIDs(line)
		if err != nil {
			return nil, err
		}
		if pid != 0 {
			byPID[pid] = comm
		}
	}
	var procs []Process
	for line := range strings.Lines(listing) {
		pid, ppid, rest, err := splitIDs(line)
		if err != nil {
			return nil, err
		}
		if pid == 0 {
			continue
		}
		user, args, _ := strings.Cut(rest, " ")
		if user == "" {
			return nil, fmt.Errorf("unexpected ps line, without a user: %q", line)
		}
		comm, ok := byPID[pid]
		if !ok {
			continue
		}
		procs = append(procs, Process{PID: pid, PPID: ppid, User: user, Comm: comm, Args: strings.TrimSpace(args)})
	}
	layOut(procs)
	return procs, nil
}

// splitIDs splits a ps line into its PID, its PPID, and the rest, with
// control characters replaced. An empty line gives a PID of 0.
func splitIDs(line string) (pid, ppid int, rest string, err error) {
	line = strings.TrimSpace(printable(strings.TrimRight(line, "\n")))
	if line == "" {
		return 0, 0, "", nil
	}
	pidField, rest, _ := strings.Cut(line, " ")
	ppidField, rest, _ := strings.Cut(strings.TrimLeft(rest, " "), " ")
	// Only positive PIDs: kill(2) treats 0 and negative values as process
	// groups, and -1 as every process the user may signal.
	pid, err = strconv.Atoi(pidField)
	if err != nil || pid <= 0 {
		return 0, 0, "", fmt.Errorf("unexpected PID in ps line: %q", line)
	}
	ppid, err = strconv.Atoi(ppidField)
	if err != nil || ppid < 0 {
		return 0, 0, "", fmt.Errorf("unexpected PPID in ps line: %q", line)
	}
	return pid, ppid, strings.TrimSpace(rest), nil
}

// commWidth is the most columns a command name takes on screen. ps on macOS
// gives argv[0], often a long path, and cut it to 16 when it was not the
// last column; uncut, one long name widened the frozen columns of every row
// until the command lines were off screen. Linux keeps names to 15.
const commWidth = 16

// layOut sets each process's Identity, and its Raw line with the columns
// padded to the widest value among procs, as ps pads them.
func layOut(procs []Process) {
	var pidW, ppidW, userW, commW int
	for _, p := range procs {
		pidW = max(pidW, len(strconv.Itoa(p.PID)))
		ppidW = max(ppidW, len(strconv.Itoa(p.PPID)))
		userW = max(userW, widths.StringWidth(p.User))
		commW = max(commW, min(widths.StringWidth(p.Comm), commWidth))
	}
	for i := range procs {
		p := &procs[i]
		p.Identity = strings.Join([]string{strconv.Itoa(p.PID), p.User, p.Comm, p.Args}, "\t")
		comm := widths.Truncate(p.Comm, commWidth, "")
		frozen := fmt.Sprintf("%*d %*d %s %s ", pidW, p.PID, ppidW, p.PPID, pad(p.User, userW), pad(comm, commW))
		p.Frozen = utf8.RuneCountInString(frozen)
		p.Raw = frozen + p.Args
	}
}

// pad pads s with spaces to width columns.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-widths.StringWidth(s), 0))
}

// printable replaces control characters and invalid UTF-8 in a ps line with
// "?", as procps prints them on Linux. ps on macOS and FreeBSD can print a
// command or command line as it is, so a process could otherwise put
// terminal control sequences in it and repaint kil's screen or the terminal
// kil reports to. Bidirectional controls and the line and paragraph
// separators, which procps keeps, become "?" too, as they can reorder or
// break the line on screen so that it reads as another command.
func printable(line string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(line, "\uFFFD") {
		if r == utf8.RuneError || unsafeOnScreen(r) {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// unsafeOnScreen reports whether r, written to a terminal, could do more
// than show a character. internal/ui draws rows with the same rule.
func unsafeOnScreen(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}

// HideDescendants returns procs without self and its descendants, so kil
// does not offer to signal itself or the ps it runs.
func HideDescendants(procs []Process, self int) []Process {
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}
	var kept []Process
	for _, p := range procs {
		if !descendsFrom(p.PID, self, parent) {
			kept = append(kept, p)
		}
	}
	return kept
}

func descendsFrom(pid, ancestor int, parent map[int]int) bool {
	// The step cap guards against PPID cycles in a snapshot taken while
	// processes are exiting.
	for steps := 0; steps <= len(parent) && pid != 0; steps++ {
		if pid == ancestor {
			return true
		}
		next, ok := parent[pid]
		if !ok {
			return false
		}
		pid = next
	}
	return false
}

// psSyntax lists the variables procps reads its option syntax from. With
// BSD syntax, such as PS_PERSONALITY=bsd, it rejects kil's options.
var psSyntax = []string{"PS_PERSONALITY", "CMD_ENV", "I_WANT_A_BROKEN_PS"}

// ps returns a ps command printing columns of the selected processes.
func ps(columns string, selection ...string) *exec.Cmd {
	cmd := exec.Command("ps", append(append([]string{psWide}, selection...), "-o", columns)...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(psSyntax, name)
	})
	return cmd
}

// output runs ps with columns for the selected processes. ps, on Linux and
// macOS, exits with status 1 and prints nothing when no process is
// selected, which gives no output and no error. It also exits with 1 on
// errors, but then says why, which is part of the error.
func output(columns string, selection ...string) (string, error) {
	cmd := ps(columns, selection...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && stderr.Len() == 0 {
		return "", nil
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("ps failed: %w: %s", err, msg)
		}
		return "", fmt.Errorf("ps failed: %w", err)
	}
	return string(out), nil
}

// snapshot lists the selected processes with both ps runs.
func snapshot(selection ...string) ([]Process, error) {
	listing, err := output(listColumns(), selection...)
	if err != nil {
		return nil, err
	}
	names, err := output(nameColumns, selection...)
	if err != nil {
		return nil, err
	}
	return Parse(listing, names)
}

// List returns every process except self and its descendants. Pass 0 to
// hide nothing. A ps failure is an error rather than a short list, because
// a partial list could mislead the user about what is running.
func List(self int) ([]Process, error) {
	procs, err := snapshot("-A")
	if err != nil {
		return nil, err
	}
	if self == 0 {
		return procs, nil
	}
	return HideDescendants(procs, self), nil
}

// Current returns the identity of pid now, or false if pid does not exist
// or changed between the ps runs. A ps that fails is an error rather than a
// missing process, so that kil does not report a process it could not check
// as one that exited.
func Current(pid int) (string, bool, error) {
	procs, err := snapshot("-p", strconv.Itoa(pid))
	if err != nil {
		return "", false, err
	}
	switch len(procs) {
	case 0:
		return "", false, nil
	case 1:
		return procs[0].Identity, true, nil
	default:
		return "", false, fmt.Errorf("ps listed %d processes for PID %d", len(procs), pid)
	}
}
