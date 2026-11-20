package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// When KIL_TEST_LISTEN is set, this test is a process that holds a socket:
// it listens with network KIL_TEST_LISTEN ("tcp4", "tcp6", or "udp4") on
// KIL_TEST_ADDR and writes the port it got to KIL_TEST_PORTFILE. With
// KIL_TEST_SHARE_TAG, it also starts a child named by that tag that
// inherits the socket, as a forked worker does, and writes the child's PID
// to KIL_TEST_PORTFILE.child.
func TestListenerChild(t *testing.T) {
	network := os.Getenv("KIL_TEST_LISTEN")
	if network == "" {
		t.Skip("run by the port tests")
	}
	addr := os.Getenv("KIL_TEST_ADDR")
	var port int
	var file *os.File
	var err error
	if strings.HasPrefix(network, "udp") {
		var c *net.UDPConn
		c, err = net.ListenUDP(network, mustUDPAddr(addr))
		if remote := os.Getenv("KIL_TEST_UDP_CONNECT"); err == nil && remote != "" {
			// A UDP socket connected after binding keeps its port.
			local := c.LocalAddr().(*net.UDPAddr)
			_ = c.Close()
			c, err = net.DialUDP(network, local, mustUDPAddr(remote))
		}
		if err == nil {
			port = c.LocalAddr().(*net.UDPAddr).Port
			file, err = c.File()
		}
	} else {
		var l *net.TCPListener
		l, err = net.ListenTCP(network, mustTCPAddr(addr))
		if err == nil {
			port = l.Addr().(*net.TCPAddr).Port
			file, err = l.File()
		}
	}
	if err != nil {
		panic(err)
	}
	portfile := os.Getenv("KIL_TEST_PORTFILE")
	if tag := os.Getenv("KIL_TEST_SHARE_TAG"); tag != "" {
		child := exec.Command(os.Args[0], "-test.run=^TestSleeperChild$", tag)
		child.Env = append(os.Environ(), "KIL_TEST_SLEEPER=1", "KIL_TEST_LISTEN=")
		child.ExtraFiles = []*os.File{file}
		if err := child.Start(); err != nil {
			panic(err)
		}
		if err := os.WriteFile(portfile+".child", []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			panic(err)
		}
	}
	if err := os.WriteFile(portfile, []byte(strconv.Itoa(port)), 0o600); err != nil {
		panic(err)
	}
	time.Sleep(time.Minute)
}

// When KIL_TEST_DIAL is set, this test is a client connected to the TCP
// address it names, as a browser on a dev server's port.
func TestClientChild(t *testing.T) {
	addr := os.Getenv("KIL_TEST_DIAL")
	if addr == "" {
		t.Skip("run by the port tests")
	}
	d := net.Dialer{}
	if from := os.Getenv("KIL_TEST_DIAL_FROM"); from != "" {
		d.LocalAddr = mustTCPAddr(from)
	}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}
	defer func() { _ = c.Close() }()
	if err := os.WriteFile(os.Getenv("KIL_TEST_PORTFILE"), []byte("connected"), 0o600); err != nil {
		panic(err)
	}
	time.Sleep(time.Minute)
}

func mustTCPAddr(s string) *net.TCPAddr {
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		panic(err)
	}
	return a
}

func mustUDPAddr(s string) *net.UDPAddr {
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		panic(err)
	}
	return a
}

// startHelper starts this test binary as the child test run names, with
// env, named by tag, and waits for it to write its file, which it returns.
func startHelper(t *testing.T, run, tag string, env ...string) (*target, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "port")
	cmd := exec.Command(os.Args[0], "-test.run=^"+run+"$", tag)
	cmd.Env = append(append(os.Environ(), "KIL_TEST_PORTFILE="+file), env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &target{cmd: cmd, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-p.exited
	})
	var content string
	waitUntil(t, run+" to start", func() bool {
		b, err := os.ReadFile(file)
		content = string(b)
		return err == nil && content != ""
	})
	return p, file
}

// startListener starts a process holding a socket of network on addr and
// returns it with the port it got.
func startListener(t *testing.T, tag, network, addr string, env ...string) (*target, int) {
	t.Helper()
	p, port, _ := startListenerFile(t, tag, network, addr, env...)
	return p, port
}

func startListenerFile(t *testing.T, tag, network, addr string, env ...string) (*target, int, string) {
	t.Helper()
	p, file := startHelper(t, "TestListenerChild", tag, append([]string{"KIL_TEST_LISTEN=" + network, "KIL_TEST_ADDR=" + addr}, env...)...)
	b, _ := os.ReadFile(file)
	port, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return p, port, file
}

// pickAll selects every row kil shows and presses Enter.
func pickAll(k *ptyKil, header string) {
	k.t.Helper()
	k.waitFor(0, header)
	time.Sleep(500 * time.Millisecond)
	k.send("\x1ba")
	k.enter(header)
}

// signalled returns the PIDs kil reported as sent signal 0, in order.
func signalled(out string) []string {
	var pids []string
	for line := range strings.Lines(out) {
		if rest, ok := strings.CutPrefix(line, "sent signal 0: "); ok {
			pids = append(pids, strings.Fields(rest)[0])
		}
	}
	return pids
}

// checkSignalled checks kil ended with status 0, nothing on stderr, and
// signal 0 sent to exactly the processes want, in any order.
func checkSignalled(t *testing.T, k *ptyKil, want ...int) {
	t.Helper()
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitOK {
		t.Errorf("exit status %d, want %d", code, exitOK)
	}
	if k.stderr() != "" {
		t.Errorf("stderr = %q, want nothing", k.stderr())
	}
	got := signalled(k.stdout())
	var wantS []string
	for _, pid := range want {
		wantS = append(wantS, strconv.Itoa(pid))
	}
	slices.Sort(got)
	slices.Sort(wantS)
	if !slices.Equal(got, wantS) {
		t.Errorf("kil signalled %v, want %v; it reported:\n%s", got, wantS, k.stdout())
	}
}

// `kil :PORT` lists only the processes that listen on the port, over
// IPv4, over IPv6, over both, or over UDP, and not clients connected to it
// nor other processes. Signal 0 keeps a broken filter from ending the
// user's processes.
func TestPortListsOnlyItsListeners(t *testing.T) {
	tests := []struct{ network, addr, dial, connect string }{
		{"tcp4", "127.0.0.1:0", "127.0.0.1", ""},
		{"tcp6", "[::1]:0", "::1", ""},
		{"tcp", ":0", "127.0.0.1", ""},
		{"udp4", "127.0.0.1:0", "", ""},
		{"udp6", "[::1]:0", "", ""},
		{"udp4", "127.0.0.1:0", "", "127.0.0.1:9"},
	}
	for _, tt := range tests {
		t.Run(tt.network+" "+tt.addr+" "+tt.connect, func(t *testing.T) {
			tag := fmt.Sprintf("kilport%d", time.Now().UnixNano())
			listener, port := startListener(t, tag+"listener", tt.network, tt.addr, "KIL_TEST_UDP_CONNECT="+tt.connect)
			// The others share the tag, so only the port leaves them out.
			bystander := startPlain(t, tag+"bystander")
			var client *target
			if tt.dial != "" {
				client, _ = startHelper(t, "TestClientChild", tag+"client", "KIL_TEST_DIAL="+net.JoinHostPort(tt.dial, strconv.Itoa(port)))
			}
			k := startKilWith(t, "KIL_SIGNAL=0", ":"+strconv.Itoa(port))
			// Alt-A selects every row on screen, so what Enter signals is
			// what kil listed. The rows are cut before the tag, so the PID
			// tells that the list is drawn.
			k.waitFor(0, strconv.Itoa(listener.pid()))
			pickAll(k, "Enter signal 0")
			checkSignalled(t, k, listener.pid())
			if !bystander.alive() || (client != nil && !client.alive()) {
				t.Error("a process not listening on the port ended")
			}
		})
	}
}

// A worker that inherited the listening socket holds the port too.
func TestPortListsEveryHolderOfTheSocket(t *testing.T) {
	tag := fmt.Sprintf("kilport%d", time.Now().UnixNano())
	parent, port, file := startListenerFile(t, tag+"parent", "tcp4", "127.0.0.1:0", "KIL_TEST_SHARE_TAG="+tag+"worker")
	b, err := os.ReadFile(file + ".child")
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := strconv.Atoi(string(b))
	t.Cleanup(func() { _ = syscall.Kill(worker, syscall.SIGKILL) })
	k := startKilWith(t, "KIL_SIGNAL=0", ":"+strconv.Itoa(port))
	k.waitFor(0, strconv.Itoa(worker))
	pickAll(k, "Enter signal 0")
	checkSignalled(t, k, parent.pid(), worker)
}

// Several ports list the processes on any of them, and the words around
// them are the query.
func TestPortsAndAQuery(t *testing.T) {
	tag := fmt.Sprintf("kilport%d", time.Now().UnixNano())
	a, portA := startListener(t, tag+"alpha", "tcp4", "127.0.0.1:0")
	b, portB := startListener(t, tag+"beta", "udp4", "127.0.0.1:0")
	_, portC := startListener(t, tag+"gamma", "tcp4", "127.0.0.1:0")
	_ = portC
	k := startKilWith(t, "KIL_SIGNAL=0", ":"+strconv.Itoa(portA), ":"+strconv.Itoa(portB))
	k.waitFor(0, strconv.Itoa(b.pid()))
	pickAll(k, "Enter signal 0")
	checkSignalled(t, k, a.pid(), b.pid())

	// The query, before or after the ports, narrows the processes on them.
	k = startKilWith(t, "KIL_SIGNAL=0", tag+"beta", ":"+strconv.Itoa(portA), ":"+strconv.Itoa(portB))
	k.waitFor(0, strconv.Itoa(b.pid()))
	pickAll(k, "Enter signal 0")
	checkSignalled(t, k, b.pid())
}

// Ctrl-R reads the port again, so a server restarted on it shows, while a
// selection of the server that exited stays pinned and is skipped.
func TestPortIsReadAgainOnRefresh(t *testing.T) {
	tag := fmt.Sprintf("kilport%d", time.Now().UnixNano())
	first, port := startListener(t, tag+"first", "tcp4", "127.0.0.1:0")
	k := startKilWith(t, "KIL_SIGNAL=0", ":"+strconv.Itoa(port))
	k.waitFor(0, strconv.Itoa(first.pid()))
	time.Sleep(500 * time.Millisecond)
	k.send("\t")
	time.Sleep(300 * time.Millisecond)
	_ = first.cmd.Process.Kill()
	<-first.exited
	second, _ := startListener(t, tag+"second", "tcp4", "127.0.0.1:"+strconv.Itoa(port))
	from := k.mark()
	k.send("\x12")
	k.waitFor(from, strconv.Itoa(second.pid()))
	time.Sleep(500 * time.Millisecond)
	// Alt-A adds the new server to the pinned selection.
	k.send("\x1ba")
	k.enter("Enter signal 0")
	waitUntil(t, "kil to end", k.ended)
	if code := k.code(); code != exitProblems {
		t.Errorf("exit status %d, want %d", code, exitProblems)
	}
	if got := signalled(k.stdout()); !slices.Equal(got, []string{strconv.Itoa(second.pid())}) {
		t.Errorf("kil signalled %v, want only PID %d:\n%s", got, second.pid(), k.stdout())
	}
	checkReports(t, "stderr", k.stderr(), first.pid(), "skipped: process changed or exited")
}

// With nothing on the port, kil says so and ends before it takes the
// terminal, as there is nothing to pick from.
func TestPortWithNothingOnIt(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	code, stdout, stderr := runKilProcess(t, ":"+strconv.Itoa(port))
	want := fmt.Sprintf("kil: no process found on port %d\n", port)
	if code != exitProblems || stderr != want || stdout != "" {
		t.Errorf("kil :%d = %d, stdout %q, stderr %q; want %d and %q", port, code, stdout, stderr, exitProblems, want)
	}
}

// A client that chose the port as its source port is not on the port as a
// server, so kil finds nothing there.
func TestPortUsedAsASourcePort(t *testing.T) {
	tag := fmt.Sprintf("kilport%d", time.Now().UnixNano())
	_, server := startListener(t, tag+"server", "tcp4", "127.0.0.1:0")
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	startHelper(t, "TestClientChild", tag+"client",
		"KIL_TEST_DIAL=127.0.0.1:"+strconv.Itoa(server), "KIL_TEST_DIAL_FROM=127.0.0.1:"+strconv.Itoa(port))
	code, _, stderr := runKilProcess(t, ":"+strconv.Itoa(port))
	want := fmt.Sprintf("kil: no process found on port %d\n", port)
	if code != exitProblems || stderr != want {
		t.Errorf("kil :%d = %d, %q; want %d and %q", port, code, stderr, exitProblems, want)
	}
}

// runKilProcess runs kil as a process of its own, without a terminal, so
// that the processes this test started are not kil's descendants, which
// kil hides, and returns its status and reports.
func runKilProcess(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestKilChild$")
	cmd.Env = append(os.Environ(), "KIL_TEST_KIL=1", "KIL_TEST_DIR="+dir, "KIL_TEST_SPLIT=1", "KIL_TEST_ARGS="+strings.Join(args, "\n"))
	_ = cmd.Run()
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	code, err := strconv.Atoi(read("code"))
	if err != nil {
		t.Fatalf("kil did not record its status: %v", err)
	}
	return code, read("stdout"), read("stderr")
}
