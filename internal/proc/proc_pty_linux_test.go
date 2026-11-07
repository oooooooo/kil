package proc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// When KIL_TEST_LIST_CHILD is set, this test lists processes on the
// terminal it was started on and fails if listing fails.
func TestListChild(t *testing.T) {
	if os.Getenv("KIL_TEST_LIST_CHILD") == "" {
		t.Skip("run by TestListOnAOneRowTerminal")
	}
	if _, err := List(0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Current(os.Getpid()); err != nil {
		t.Fatal(err)
	}
}

// procps reads the screen size from the controlling terminal and warns on
// stderr about one it finds bogus, such as one row, while it still lists
// every process. kil, which asks for whole lines, must still list them.
func TestListOnAOneRowTerminal(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	defer func() { _ = master.Close() }()
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 1, Col: 80}); err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slave.Close() }()
	var out bytes.Buffer
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	child := exec.Command(os.Args[0], "-test.run=^TestListChild$", "-test.v")
	child.Env = append(os.Environ(), "KIL_TEST_LIST_CHILD=1")
	child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := child.Run(); err != nil {
		mu.Lock()
		defer mu.Unlock()
		t.Errorf("listing on a one-row terminal failed: %v\n%s", err, out.String())
	}
}
