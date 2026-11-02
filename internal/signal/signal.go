// Package signal validates KIL_SIGNAL and signals processes.
package signal

import (
	"fmt"
	"regexp"
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
	if sig := unix.SignalNum(name); sig != 0 {
		return sig, nil
	}
	return 0, fmt.Errorf("invalid signal %q", s)
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
)

func (o Outcome) String() string {
	switch o {
	case Sent:
		return "sent"
	case Skipped:
		return "skipped"
	default:
		return "failed"
	}
}

// Send signals pid only if current(pid) still returns want, the line the
// user selected, so that a process that exited and whose PID was reused by
// an unrelated process is not signalled.
func Send(pid int, sig syscall.Signal, want string, current func(int) (string, bool)) Outcome {
	// kill(2) treats 0 and negative PIDs as process groups, and -1 as every
	// process the user may signal.
	if pid <= 0 {
		return Skipped
	}
	return send(pid, sig, func() bool {
		line, ok := current(pid)
		return ok && line == want
	})
}

func outcomeOf(err error) Outcome {
	switch err {
	case nil:
		return Sent
	case unix.ESRCH:
		return Skipped
	default:
		return Failed
	}
}
