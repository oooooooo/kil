// Package signal validates KIL_SIGNAL and signals processes.
package signal

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var digits = regexp.MustCompile(`^[0-9]+$`)

// Parse converts a signal number or name (TERM, SIGTERM, term) into a
// signal. An empty string means SIGKILL, kil's default. Numbers without a
// name, such as Linux real-time signals, are rejected so that every
// accepted value can be shown to the user by name.
func Parse(s string) (syscall.Signal, error) {
	if s == "" {
		return syscall.SIGKILL, nil
	}
	if digits.MatchString(s) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("invalid signal %q", s)
		}
		sig := syscall.Signal(n)
		if n != 0 && unix.SignalName(sig) == "" {
			return 0, fmt.Errorf("invalid signal %q", s)
		}
		return sig, nil
	}
	name := strings.ToUpper(s)
	if !strings.HasPrefix(name, "SIG") {
		name = "SIG" + name
	}
	if alias, ok := aliases()[name]; ok {
		name = alias
	}
	if sig := unix.SignalNum(name); sig != 0 {
		return sig, nil
	}
	return 0, fmt.Errorf("invalid signal %q", s)
}

// aliases maps the other names signal.h gives some signals to the names
// kil reports them by: SIGIOT is SIGABRT on Linux and macOS, and Linux also
// has SIGCLD for SIGCHLD and SIGPOLL, which procps's kill accepts, for
// SIGIO.
func aliases() map[string]string {
	a := map[string]string{"SIGIOT": "SIGABRT"}
	if runtime.GOOS == "linux" {
		a["SIGCLD"] = "SIGCHLD"
		a["SIGPOLL"] = "SIGIO"
	}
	return a
}

// Label returns the name kil reports for sig, e.g. "SIGKILL".
func Label(sig syscall.Signal) string {
	if sig == 0 {
		return "signal 0"
	}
	return unix.SignalName(sig)
}

// An Outcome is the result of Send.
type Outcome int

const (
	// Sent means the signal was delivered.
	Sent Outcome = iota
	// Skipped means the process exited or changed, so it was not signalled.
	Skipped
	// Failed means the process was there but could not be signalled, e.g.
	// for lack of permission.
	Failed
	// Cancelled means the caller stopped signalling before the signal was
	// sent.
	Cancelled
)

func (o Outcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case Skipped:
		return "skipped"
	case Cancelled:
		return "cancelled"
	default:
		return "failed"
	}
}

// Send signals pid only if current(pid) still returns want, the line the
// user selected, as SendAll does for one target.
func Send(pid int, sig syscall.Signal, want string, current func(int) (string, bool, error), stopped func() bool) (Outcome, error) {
	lookup := func(pids []int) (map[int]string, error) {
		found := map[int]string{}
		for _, p := range pids {
			line, ok, err := current(p)
			if err != nil {
				return nil, err
			}
			if ok {
				found[p] = line
			}
		}
		return found, nil
	}
	var outcome Outcome
	var err error
	SendAll([]Target{{pid, want}}, sig, lookup, stopped, func(_ int, o Outcome, e error) { outcome, err = o, e })
	return outcome, err
}

// outcomeOf returns the outcome of a system call that signals a process or
// opens it, and the error when that is a failure.
func outcomeOf(err error) (Outcome, error) {
	switch err {
	case nil:
		return Sent, nil
	case unix.ESRCH:
		return Skipped, nil
	default:
		return Failed, err
	}
}

// A Target is a process to signal and the identity it must still have:
// the line the user selected.
type Target struct {
	PID  int
	Want string
}

// A Lookup returns the identities of the processes with the given PIDs now.
// A process that is gone is missing from the map.
type Lookup func(pids []int) (map[int]string, error)

// batchSize is the most targets SendAll holds a pidfd open for at once,
// well below the usual limit of 1024 open files.
const batchSize = 256

// SendAll signals each target only if lookup still gives its identity, so
// that a process that exited and whose PID was reused by an unrelated one
// is not signalled, and tells done the outcome of each, in order, with the
// reason when it is Failed. Where it can, it opens a pidfd for each target
// before one lookup for all of them, rather than running ps per target,
// and signals through it, so the signal reaches the process checked even
// if its PID is reused meanwhile. Elsewhere each target is checked right
// before its signal, which leaves only that moment for the PID to change.
// stopped, if not nil, is asked before each target and right before each
// signal; when it says to stop, done hears Cancelled for that target and
// SendAll returns, leaving the rest to the caller.
func SendAll(targets []Target, sig syscall.Signal, lookup Lookup, stopped func() bool, done func(i int, o Outcome, err error)) {
	for start := 0; start < len(targets); {
		end := min(start+batchLimit(), len(targets))
		if !sendBatch(targets[start:end], start, sig, lookup, stopped, done) {
			return
		}
		start = end
	}
}

func sendBatch(targets []Target, offset int, sig syscall.Signal, lookup Lookup, stopped func() bool, done func(int, Outcome, error)) bool {
	stop := func() bool { return stopped != nil && stopped() }
	if stop() {
		done(offset, Cancelled, nil)
		return false
	}
	type opened struct {
		h       handle
		outcome Outcome
		err     error
		ok      bool
		// before is the process's command as the kernel had it before the
		// lookup, so that one replaced by exec since is not signalled.
		before string
		// alone is set for a process checked on its own right before its
		// signal, rather than with the others.
		alone bool
	}
	handles := make([]opened, len(targets))
	var pids []int
	for i, t := range targets {
		// kill(2) treats 0 and negative PIDs as process groups, and -1
		// as every process the user may signal.
		if t.PID <= 0 {
			handles[i] = opened{outcome: Skipped}
			continue
		}
		h, err := open(t.PID)
		if err != nil {
			o, err := outcomeOf(err)
			handles[i] = opened{outcome: o, err: err}
			continue
		}
		o := opened{h: h, ok: true, alone: h.byPID}
		if !o.alone {
			// Read before the lookup, so that an exec at any time after
			// it shows as a change.
			o.before, o.alone = h.command(t.PID)
			o.alone = !o.alone
		}
		if !o.alone {
			pids = append(pids, t.PID)
		}
		handles[i] = o
	}
	defer func() {
		for _, o := range handles {
			if o.ok {
				o.h.close()
			}
		}
	}()
	var found map[int]string
	var lookupErr error
	if len(pids) > 0 {
		if stop() {
			done(offset, Cancelled, nil)
			return false
		}
		found, lookupErr = lookup(pids)
	}
	for i, t := range targets {
		if stop() {
			done(offset+i, Cancelled, nil)
			return false
		}
		o := handles[i]
		if !o.ok {
			done(offset+i, o.outcome, o.err)
			continue
		}
		now, err := found, lookupErr
		if o.alone {
			now, err = lookup([]int{t.PID})
		}
		if err != nil {
			done(offset+i, Failed, fmt.Errorf("checking the process: %w", err))
			continue
		}
		if line, ok := now[t.PID]; !ok || line != t.Want {
			done(offset+i, Skipped, nil)
			continue
		}
		// A process checked with the others may have been replaced by exec
		// while earlier targets were signalled; a pidfd keeps the process
		// but not its command. As a process can also rewrite its command
		// line or name itself, a change is checked again on its own.
		after, ok := "", true
		if !o.alone {
			after, ok = o.h.command(t.PID)
		}
		// The check can take a while, as it runs ps, so a stop that came
		// meanwhile still keeps the signal from being sent.
		if stop() {
			done(offset+i, Cancelled, nil)
			return false
		}
		if !o.alone {
			if !ok || after != o.before {
				now, err := lookup([]int{t.PID})
				if err != nil {
					done(offset+i, Failed, fmt.Errorf("checking the process: %w", err))
					continue
				}
				if line, ok := now[t.PID]; !ok || line != t.Want {
					done(offset+i, Skipped, nil)
					continue
				}
				if stop() {
					done(offset+i, Cancelled, nil)
					return false
				}
			}
		}
		out, err := outcomeOf(o.h.signal(t.PID, sig))
		done(offset+i, out, err)
	}
	return true
}
