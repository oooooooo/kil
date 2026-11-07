package signal

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// A handle signals one process: through a pidfd opened before the check,
// so the signal goes to the process that was checked even if it exits and
// its PID is reused meanwhile, or by its PID, checked right before the
// signal. The PID is used without pidfd, as on kernels older than 5.3 or
// where seccomp denies pidfd_open or pidfd_send_signal, as Docker's older
// default profile does, which leaves that moment open, as on macOS.
type handle struct {
	fd    int
	byPID bool
}

func open(pid int) (handle, error) {
	fd, err := pidfdOpen(pid, 0)
	if fallsBack(err) {
		return handle{byPID: true}, nil
	}
	if err != nil {
		return handle{}, err
	}
	return handle{fd: fd}, nil
}

func (h handle) signal(pid int, sig syscall.Signal) error {
	if h.byPID {
		return kill(pid, sig)
	}
	err := pidfdSendSignal(h.fd, sig, nil, 0)
	if fallsBack(err) {
		// The process was checked through the pidfd, which kil can open
		// but not signal through, so its PID is signalled, as on a system
		// without pidfd.
		return kill(pid, sig)
	}
	return err
}

// command returns the process's command line and name as the kernel has
// them, which change when it is replaced by exec, or false if they cannot
// be read, as when /proc hides other users' processes.
func (h handle) command(pid int) (string, bool) { return readCommand(pid) }

func procCommand(pid int) (string, bool) {
	dir := "/proc/" + strconv.Itoa(pid) + "/"
	cmdline, err := os.ReadFile(dir + "cmdline")
	if err != nil {
		return "", false
	}
	comm, err := os.ReadFile(dir + "comm")
	if err != nil {
		return "", false
	}
	return string(comm) + "\x00" + string(cmdline), true
}

func (h handle) close() {
	if !h.byPID {
		_ = unix.Close(h.fd)
	}
}

// pidfdSendSignal is pidfd_send_signal(2). Tests replace it to make a signal
// fail.
var pidfdSendSignal = unix.PidfdSendSignal

// pidfdOpen is pidfd_open(2). Tests replace it to make it fail.
var pidfdOpen = unix.PidfdOpen

// pidfdUnavailable reports whether kil cannot open a pidfd for itself, or
// signal itself through it with signal 0, which tells a system without
// pidfd, or one that denies it, from a failure that belongs to one target.
// Go's os package probes pidfd the same way. Running out of file
// descriptors or memory is not a lack of pidfd, as Rust's and Python's
// standard libraries also hold, so kil then reports the failure rather than
// give up the pidfd's protection.
func pidfdUnavailable() bool {
	fd, err := pidfdOpen(unix.Getpid(), 0)
	if err != nil {
		return !outOfResources(err)
	}
	defer func() { _ = unix.Close(fd) }()
	err = pidfdSendSignal(fd, 0, nil, 0)
	return err != nil && !outOfResources(err)
}

// fallsBack reports whether err, from opening a pidfd for the target or
// signalling through it, means kil should signal its PID instead: pidfd is
// unavailable, not the target gone nor resources short.
func fallsBack(err error) bool {
	return err != nil && err != unix.ESRCH && !outOfResources(err) && pidfdUnavailable()
}

func outOfResources(err error) bool {
	return err == unix.EMFILE || err == unix.ENFILE || err == unix.ENOMEM
}

// readCommand returns a process's command line and name as the kernel has
// them. Tests replace it.
var readCommand = procCommand

// openFiles returns the open-file limit and how many files kil has open.
// Tests replace it.
var openFiles = func() (limit, open int) {
	var r unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &r); err != nil {
		return 1024, 0
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return int(min(r.Cur, 1<<20)), 0
	}
	return int(min(r.Cur, 1<<20)), len(fds)
}

// fdReserve is the files kept free for ps, its pipes, and /proc while a
// batch holds its pidfds.
const fdReserve = 64

// batchLimit returns how many pidfds a batch may hold: no more than the
// open-file limit leaves room for, with fdReserve to spare, as a batch that
// took them all left ps unable to start and failed every target.
func batchLimit() int {
	limit, open := openFiles()
	return max(min(limit-open-fdReserve, batchSize), 1)
}
