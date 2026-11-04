package main

import (
	"os"
	ossignal "os/signal"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/ui"
)

// interrupts records the signals kil catches: SIGHUP, which ends the picker
// but not the signalling, and SIGINT, SIGTERM, and SIGQUIT, which end both.
// kil handles them itself, rather than by their default action, so that the
// terminal is restored, the processes already signalled are reported, and
// the status is the one a shell gives a command ended by the signal.
//
// The signals are recorded rather than handed over a channel, so that one
// arriving while kil is busy, such as during ps, is not lost.
type interrupts struct {
	mu sync.Mutex
	// firstSig is the first signal caught; stopSig is the first SIGINT,
	// SIGTERM, or SIGQUIT.
	firstSig, stopSig os.Signal
	// caughtCh is closed when the first signal is caught.
	caughtCh chan struct{}
	done     chan struct{}
	wg       sync.WaitGroup
}

// The signals that stop signalling.
var stopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

// watchSignals starts recording the signals kil handles itself.
func watchSignals() *interrupts {
	in := &interrupts{done: make(chan struct{})}
	// os/signal drops a signal when its channel is full, so SIGHUPs get
	// their own channel and cannot crowd out a SIGTERM.
	hups := make(chan os.Signal, 1)
	stops := make(chan os.Signal, 1)
	ossignal.Notify(hups, syscall.SIGHUP)
	ossignal.Notify(stops, stopSignals...)
	in.wg.Add(1)
	go func() {
		defer in.wg.Done()
		defer ossignal.Stop(hups)
		defer ossignal.Stop(stops)
		for {
			select {
			case s := <-hups:
				in.catch(s)
			case s := <-stops:
				in.catch(s)
				// A second one ends kil at once by its default action,
				// in case kil cannot stop, as when ps hangs.
				ossignal.Reset(stopSignals...)
			case <-in.done:
				return
			}
		}
	}()
	return in
}

// close stops recording signals. Signals arriving later take their default
// action.
func (in *interrupts) close() {
	close(in.done)
	in.wg.Wait()
}

func (in *interrupts) catch(s os.Signal) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.firstSig == nil {
		in.firstSig = s
		close(in.caught())
	}
	if in.stopSig == nil && s != syscall.SIGHUP {
		in.stopSig = s
	}
}

// caught returns a channel closed once a signal is caught. The caller holds
// in.mu or owns in.
func (in *interrupts) caught() chan struct{} {
	if in.caughtCh == nil {
		in.caughtCh = make(chan struct{})
	}
	return in.caughtCh
}

// whenCaught returns a channel closed once a signal is caught.
func (in *interrupts) whenCaught() <-chan struct{} {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.caught()
}

// first returns the first signal caught, or nil.
func (in *interrupts) first() os.Signal {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.firstSig
}

// stopping returns the first SIGINT, SIGTERM, or SIGQUIT caught, or nil.
func (in *interrupts) stopping() os.Signal {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.stopSig
}

// ended returns the exit status for the signals caught, and whether any
// was: a signal that stops signalling wins over an earlier SIGHUP.
func (in *interrupts) ended() (int, bool) {
	if s := in.stopping(); s != nil {
		return exitSignalled(s), true
	}
	if s := in.first(); s != nil {
		return exitSignalled(s), true
	}
	return 0, false
}

// exitSignalled returns the status a shell gives a command ended by s.
func exitSignalled(s os.Signal) int {
	n, _ := s.(syscall.Signal)
	return 128 + int(n)
}

// currentLine returns a process's line as kil checks it before signalling.
// Tests replace it to act while the check runs.
var currentLine = proc.Current

// sender returns the function that signals the rows of a partial Enter,
// holding the reports in b until the picker has ended.
func sender(b *batches, sig syscall.Signal, in *interrupts) func([]picker.Item) {
	return func(batch []picker.Item) {
		b.code = max(b.code, signalAll(batch, sig, b.stdout(), b.stderr(), in))
		b.sent += len(batch)
	}
}

// suspender suspends kil for Ctrl-Z or SIGTSTP. Bubble Tea's own suspend
// let keys typed as kil resumed reach the picker before it heard that kil
// had resumed, so Enter did not wait for the screen to be drawn again; it
// also stopped kil by a SIGTSTP that kil, catching SIGTSTP, never received.
type suspender struct {
	in *interrupts
	// canSuspend reports whether a shell can bring kil back once stopped.
	canSuspend func() bool
	// release gives the terminal back to the shell, stop stops kil until
	// it is continued, and restore takes the terminal again.
	release, stop, restore func() error
}

// filter is a Bubble Tea filter. It runs on Bubble Tea's event loop, so no
// key reaches the picker while kil suspends, and the tea.ResumeMsg it
// returns reaches the picker before any key typed as kil resumed.
func (s suspender) filter(m tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.SuspendMsg); !ok {
		return msg
	}
	// Ctrl-Z's tea.SuspendMsg arrives after the key, so kil may already be
	// ending.
	if model, ok := m.(ui.Model); !ok || model.Outcome() != ui.Running || s.in.first() != nil {
		return nil
	}
	if !s.canSuspend() {
		return nil
	}
	if err := s.release(); err == nil {
		_ = s.stop()
	}
	// The terminal is taken again even after a failed release, which may
	// have released part of it.
	_ = s.restore()
	return tea.ResumeMsg{}
}

// jobControlled reports whether a shell can bring kil back once stopped: its
// parent is in the same session but another process group, as an
// interactive shell is. Without one, as when a terminal emulator or tmux
// runs kil directly, kil's process group is orphaned and stopping it would
// leave it stopped for good.
func jobControlled() bool {
	ppid := os.Getppid()
	sid, err := unix.Getsid(0)
	if err != nil {
		return false
	}
	parentSid, err := unix.Getsid(ppid)
	if err != nil {
		return false
	}
	parentPgid, err := unix.Getpgid(ppid)
	if err != nil {
		return false
	}
	return parentSid == sid && parentPgid != unix.Getpgrp()
}

// stopJob stops kil's process group, as the terminal does for Ctrl-Z, and
// returns once it is continued. kil catches SIGTSTP, and the Go runtime
// keeps catching it even after signal.Stop, so the group is stopped with
// SIGSTOP, which cannot be caught.
func stopJob() error {
	cont := make(chan os.Signal, 1)
	ossignal.Notify(cont, syscall.SIGCONT)
	defer ossignal.Stop(cont)
	if err := unix.Kill(0, unix.SIGSTOP); err != nil {
		return err
	}
	// SIGSTOP may stop kil after kill(2) returns, so kil waits to be
	// continued.
	<-cont
	return nil
}
