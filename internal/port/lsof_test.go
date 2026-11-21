package port

import (
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func localPort(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return n
}

// lsof, which kil runs on macOS, gives the sockets holding a port: a TCP
// listener, a UDP socket bound to it, connected or not, and not a UDP or
// TCP client whose remote end is the port. It runs here with the lsof of
// this system, whose flags and output are those of macOS's.
func TestLsofHolders(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tcp.Close() }()
	tcpPort := localPort(t, tcp.Addr().String())
	tcpClient, err := net.Dial("tcp4", tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tcpClient.Close() }()

	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udp.Close() }()
	udpPort := localPort(t, udp.LocalAddr().String())
	udpClient, err := net.Dial("udp4", udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udpClient.Close() }()
	connected, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, udp.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connected.Close() }()
	connectedPort := localPort(t, connected.LocalAddr().String())

	// The client to the TCP listener holds its source port, but only as
	// one end of a connection, not as a server on it.
	sourcePort := localPort(t, tcpClient.LocalAddr().String())

	got, err := lsofHolders([]int{tcpPort, udpPort, connectedPort, sourcePort})
	if err != nil {
		t.Fatalf("lsofHolders = %v", err)
	}
	var mine []string
	for _, h := range got {
		if h.pid == os.Getpid() {
			mine = append(mine, h.local)
		}
	}
	slices.Sort(mine)
	want := []string{
		"127.0.0.1:" + strconv.Itoa(connectedPort),
		"127.0.0.1:" + strconv.Itoa(tcpPort),
		"127.0.0.1:" + strconv.Itoa(udpPort),
	}
	slices.Sort(want)
	if !slices.Equal(mine, want) {
		t.Errorf("lsofHolders gave this process's sockets %q, want %q", mine, want)
	}

	// A TCP port leaves the UDP selector for it unmatched, which lsof
	// tells with status 1.
	got, err = lsofHolders([]int{tcpPort})
	if err != nil || !slices.ContainsFunc(got, func(h holder) bool { return h.pid == os.Getpid() }) {
		t.Errorf("lsofHolders(TCP port only) = %v, %v; want this process", got, err)
	}

}

// lsof ending with 1 and printing nothing, as when no socket matches, is no
// holder and no error. lsof is replaced, as a port that no socket has on
// any address cannot be kept so while lsof runs.
func TestLsofFindsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/lsof", []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := lsofHolders([]int{3000})
	if err != nil || len(got) != 0 {
		t.Errorf("lsofHolders(nothing found) = %v, %v; want nothing", got, err)
	}
}

// lsof on macOS takes at most 100 -i selectors, two per port, so ports are
// looked up in batches; a batch that matches nothing does not hide the
// holders another batch finds.
func TestLsofHoldersInBatches(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof")
	}
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tcp.Close() }()
	saved := lsofBatch
	defer func() { lsofBatch = saved }()
	lsofBatch = 1
	got, err := lsofHolders([]int{freeTCPAndUDPPort(t), localPort(t, tcp.Addr().String())})
	if err != nil || !slices.ContainsFunc(got, func(h holder) bool { return h.pid == os.Getpid() }) {
		t.Errorf("lsofHolders in batches of one = %v, %v; want this process", got, err)
	}
}

// freeTCPAndUDPPort returns a port that no TCP or UDP socket has: one free
// for TCP may be taken for UDP.
func freeTCPAndUDPPort(t *testing.T) int {
	t.Helper()
	for range 100 {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := localPort(t, l.Addr().String())
		u, err := net.ListenPacket("udp4", "127.0.0.1:"+strconv.Itoa(port))
		_ = l.Close()
		if err == nil {
			_ = u.Close()
			return port
		}
	}
	t.Fatal("no port free for both TCP and UDP")
	return 0
}

// A missing lsof is an error that names it, not a free port.
func TestLsofMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := lsofHolders([]int{3000})
	if err == nil || !strings.Contains(err.Error(), "lsof") {
		t.Errorf("lsofHolders without lsof = %v, want an error naming lsof", err)
	}
}
