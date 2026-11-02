package proc

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"testing"
)

func pids(ps []Process) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.PID
	}
	return out
}

func TestParse(t *testing.T) {
	out := "    1     0 root     init            /sbin/init\n" +
		"  202     1 alice    tmux: server    tmux new -s work\n" +
		"\n"
	got, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{
		{PID: 1, PPID: 0, Line: "1 0 root init /sbin/init",
			Raw: "    1     0 root     init            /sbin/init"},
		{PID: 202, PPID: 1, Line: "202 1 alice tmux: server tmux new -s work",
			Raw: "  202     1 alice    tmux: server    tmux new -s work"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	for _, line := range []string{"abc 1 root x x", "12", "-1 1 root x x", "0 1 root x x"} {
		if _, err := Parse(line + "\n"); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", line)
		}
	}
}

func TestHideDescendants(t *testing.T) {
	// The child (30) is listed before its parent (20), as ps may do.
	procs := []Process{
		{PID: 1, PPID: 0},
		{PID: 30, PPID: 20},
		{PID: 10, PPID: 1},
		{PID: 20, PPID: 10},
		{PID: 40, PPID: 1},
	}
	got := pids(HideDescendants(procs, 10))
	if !slices.Equal(got, []int{1, 40}) {
		t.Errorf("HideDescendants() kept %v, want [1 40]", got)
	}
}

func TestHideDescendantsSurvivesParentCycles(t *testing.T) {
	procs := []Process{{PID: 5, PPID: 6}, {PID: 6, PPID: 5}, {PID: 7, PPID: 1}}
	got := pids(HideDescendants(procs, 7))
	if !slices.Equal(got, []int{5, 6}) {
		t.Errorf("HideDescendants() kept %v, want [5 6]", got)
	}
}

func TestListShowsOtherProcessesButNotSelf(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	got, err := List(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	// The sleep is our child, so it is hidden as a descendant; its parent
	// chain must be checked against something that is not ours instead.
	if slices.Contains(pids(got), os.Getpid()) {
		t.Error("List() includes the calling process")
	}
	if slices.Contains(pids(got), cmd.Process.Pid) {
		t.Error("List() includes a child of the calling process")
	}
	if !slices.Contains(pids(got), 1) {
		t.Error("List() does not include PID 1")
	}
}

func TestCurrentMatchesListedLine(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	all, err := List(0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(p Process) bool { return p.PID == cmd.Process.Pid })
	if i < 0 {
		t.Fatalf("sleep %d not listed", cmd.Process.Pid)
	}
	line, ok := Current(cmd.Process.Pid)
	if !ok || line != all[i].Line {
		t.Errorf("Current() = %q, %v; want %q, true", line, ok, all[i].Line)
	}
}

func TestCurrentReportsExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if line, ok := Current(cmd.Process.Pid); ok {
		t.Errorf("Current(exited %s) = %q, true; want false", strconv.Itoa(cmd.Process.Pid), line)
	}
}
