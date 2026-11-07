package proc

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/oooooooo/kil/internal/width"
)

func pids(ps []Process) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.PID
	}
	return out
}

// kil reads the command name in a ps run of its own, as the last column,
// so that a name with spaces, such as "tmux: server", ends where it should,
// and lays the columns out itself.
func TestParse(t *testing.T) {
	listing := "    1     0 root     /sbin/init\n" +
		"  202     1 alice    tmux new -s work\n" +
		"\n"
	names := "    1     0 init\n  202     1 tmux: server\n"
	got, err := Parse(listing, names)
	if err != nil {
		t.Fatal(err)
	}
	want := []Process{
		{PID: 1, PPID: 0, User: "root", Comm: "init", Args: "/sbin/init",
			Identity: "1\troot\tinit\t/sbin/init",
			Raw:      "  1 0 root  init         /sbin/init", Frozen: 25},
		{PID: 202, PPID: 1, User: "alice", Comm: "tmux: server", Args: "tmux new -s work",
			Identity: "202\talice\ttmux: server\ttmux new -s work",
			Raw:      "202 1 alice tmux: server tmux new -s work", Frozen: 25},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Parse() = %+v, want %+v", got, want)
	}
}

// A process missing from one of the two ps runs exited or started in
// between, so it is left out rather than shown without a name.
func TestParseLeavesOutProcessesMissingFromARun(t *testing.T) {
	listing := "  10     1 root /a\n  30     1 root /c\n"
	names := "  10     1 a\n  40     1 d\n"
	got, err := Parse(listing, names)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pids(got), []int{10}) {
		t.Errorf("Parse() kept %v, want [10]", pids(got))
	}
}

// A process whose parent was killed between the two runs was reparented,
// and is still the same process: dropped, it would be skipped as changed
// when kil signals a parent and then its child.
func TestParseKeepsAProcessReparentedBetweenTheRuns(t *testing.T) {
	got, err := Parse("  20     1 root /b\n", "  20     5 b\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PPID != 1 || got[0].Comm != "b" {
		t.Errorf("Parse() = %+v, want process 20 with the listing's PPID 1", got)
	}
}

// A long command name, as a macOS path in argv[0], is shown cut to 16
// columns, as ps on macOS cut it, so that one long name cannot widen the
// frozen columns of every row until the command lines are off screen. The
// identity keeps the whole name.
func TestParseCutsLongCommandNamesOnScreen(t *testing.T) {
	long := "/Applications/Visual Studio Code.app/Contents/MacOS/Electron"
	got, err := Parse("  1 0 alice /x\n", "  1 0 "+long+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Parse() = %+v, want one process", got)
	}
	if want := "1 0 alice /Applications/Vi /x"; got[0].Raw != want || got[0].Frozen != len(want)-2 {
		t.Errorf("Raw = %q, Frozen = %d; want %q and %d", got[0].Raw, got[0].Frozen, want, len(want)-2)
	}
	if got[0].Comm != long || !strings.Contains(got[0].Identity, long) {
		t.Errorf("Comm = %q, Identity = %q; want the whole name in both", got[0].Comm, got[0].Identity)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	for _, line := range []string{"abc 1 root x", "12", "-1 1 root x", "0 1 root x", "12 1"} {
		if _, err := Parse(line+"\n", "12 1 x\n"); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", line)
		}
	}
	for _, line := range []string{"abc 1 x", "12", "12 x y"} {
		if _, err := Parse("12 1 root x\n", line+"\n"); err == nil {
			t.Errorf("Parse(names %q) succeeded, want an error", line)
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
	line, ok, err := Current(cmd.Process.Pid)
	if err != nil || !ok || line != all[i].Identity {
		t.Errorf("Current() = %q, %v, %v; want %q, true, nil", line, ok, err, all[i].Identity)
	}
}

func TestCurrentReportsExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if line, ok, err := Current(cmd.Process.Pid); ok || err != nil {
		t.Errorf("Current(exited %s) = %q, %v, %v; want false and no error", strconv.Itoa(cmd.Process.Pid), line, ok, err)
	}
}

// ps cuts lines to COLUMNS when it is set, which would hide the end of long
// command lines from matching and from the check before signalling.
func TestLongCommandLinesAreNotCutToColumns(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	cmd := exec.Command("sleep", "60")
	cmd.Args[0] = strings.Repeat("x", 300) + "-tail"
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	all, err := List(0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(p Process) bool { return p.PID == cmd.Process.Pid })
	if i < 0 || !strings.HasSuffix(all[i].Raw, "-tail 60") {
		t.Fatalf("List() did not include the whole command line of %d", cmd.Process.Pid)
	}
	if line, ok, err := Current(cmd.Process.Pid); err != nil || !ok || line != all[i].Identity {
		t.Errorf("Current() = %q, %v, %v; want the whole line", line, ok, err)
	}
}

// procps reads its option syntax from these variables, and with BSD syntax
// it rejects kil's options, which left kil unable to list processes. Not
// every option fails, so a stand-in ps fails whenever it is given one of
// them, and otherwise runs the real ps.
func TestPsSyntaxVariablesAreIgnored(t *testing.T) {
	real, err := exec.LookPath("ps")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ -n \"${PS_PERSONALITY+x}${CMD_ENV+x}${I_WANT_A_BROKEN_PS+x}\" ]; then exit 1; fi\n" +
		"exec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PS_PERSONALITY", "bsd")
	t.Setenv("CMD_ENV", "bsd")
	t.Setenv("I_WANT_A_BROKEN_PS", "1")

	if _, err := List(0); err != nil {
		t.Errorf("List(): %v", err)
	}
	if _, ok, err := Current(os.Getpid()); !ok || err != nil {
		t.Errorf("Current(self) found nothing: %v", err)
	}
}

// fakePS puts a ps that runs script first on PATH.
func fakePS(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// A ps that fails is not a process that exited: kil must not report a
// process it could not check as changed or exited.
func TestCurrentReportsAFailedPs(t *testing.T) {
	for name, script := range map[string]string{
		"an error message":       "echo 'ps: cannot read /proc' >&2; exit 1",
		"another status":         "exit 2",
		"output it cannot parse": "echo garbage",
	} {
		t.Run(name, func(t *testing.T) {
			fakePS(t, script)
			if line, ok, err := Current(os.Getpid()); err == nil {
				t.Errorf("Current() = %q, %v, nil; want an error", line, ok)
			}
		})
	}
	t.Run("no ps", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if line, ok, err := Current(os.Getpid()); err == nil {
			t.Errorf("Current() = %q, %v, nil; want an error", line, ok)
		}
	})
}

// The error says what ps wrote, so that the user can tell why kil could not
// check a process.
func TestCurrentErrorIncludesWhatPsWrote(t *testing.T) {
	fakePS(t, "echo 'ps: cannot read /proc' >&2; exit 1")
	if _, _, err := Current(os.Getpid()); err == nil || !strings.Contains(err.Error(), "cannot read /proc") {
		t.Errorf("Current() error = %v, want it to include what ps wrote", err)
	}
}

// ps on macOS prints a command line as it is, so a process could put
// terminal control sequences in it and repaint kil's screen or the
// terminal kil reports to. Control characters and invalid UTF-8 become
// "?" on screen, as procps prints them on Linux.
func TestParseReplacesControlCharacters(t *testing.T) {
	listing := "  101     1 alice    /bin/x \x1b]0;pwn\x07 \x1b[31mred\x7f \u009b2J \xff\n"
	got, err := Parse(listing, "  101     1 ev\x1b[2Jil\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Parse() = %v, want one process", got)
	}
	wantRaw := "101 1 alice ev?[2Jil /bin/x ?]0;pwn? ?[31mred? ?2J ?"
	if got[0].Raw != wantRaw {
		t.Errorf("Raw = %q, want %q", got[0].Raw, wantRaw)
	}
	// The identity, which is never shown, keeps them, so that the check
	// before signalling still tells processes apart.
	wantID := "101\talice\tev\x1b[2Jil\t/bin/x \x1b]0;pwn\x07 \x1b[31mred\x7f \u009b2J \xff"
	if got[0].Identity != wantID {
		t.Errorf("Identity = %q, want %q", got[0].Identity, wantID)
	}
}

// Bidirectional controls, such as U+202E RIGHT-TO-LEFT OVERRIDE, and the
// line and paragraph separators are not control characters to Unicode, but
// can reorder or break a command line on screen, so that it reads as
// another one. They become "?" as well; the zero-width joiner in emoji does
// not.
func TestParseReplacesBidiControlsAndSeparators(t *testing.T) {
	listing := "  101     1 alice    /bin/x \u202Etxt.exe \u2066a\u2069 \u200F \u2028 \u2029 \U0001F468\u200D\U0001F469\n"
	got, err := Parse(listing, "  101     1 evil\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "101 1 alice evil /bin/x ?txt.exe ?a? ? ? ? \U0001F468\u200D\U0001F469"
	if len(got) != 1 || got[0].Raw != want {
		t.Errorf("Parse() = %+v, want Raw %q", got, want)
	}
}

// A command name with spaces, as an executable named "my sleep" has, is
// kept whole, and the columns kept on screen end where the command line
// begins.
func TestListKeepsCommandNamesWithSpaces(t *testing.T) {
	dir := t.TempDir()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(dir, "my sleep")
	if err := os.Symlink(sleep, named); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(named, "60")
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
		t.Fatalf("process %d not listed", cmd.Process.Pid)
	}
	p := all[i]
	if !strings.HasSuffix(p.Comm, "my sleep") {
		t.Errorf("Comm = %q, want it to end with \"my sleep\"", p.Comm)
	}
	if got := string([]rune(p.Raw)[p.Frozen:]); got != p.Args {
		t.Errorf("Raw after the frozen columns = %q, want the command line %q", got, p.Args)
	}
}

// User names longer than 8 characters are listed whole: procps cuts them
// to 8, as "systemd+", unless asked for a wider column.
func TestListKeepsLongUserNames(t *testing.T) {
	all, err := List(0)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	for _, p := range all {
		uid, ok := procUID(p.PID)
		if !ok {
			continue
		}
		u, err := user.LookupId(uid)
		if err != nil || len(u.Username) <= 8 {
			continue
		}
		checked = true
		if p.User != u.Username {
			t.Errorf("PID %d: User = %q, want %q", p.PID, p.User, u.Username)
		}
	}
	if !checked {
		t.Skip("no process here runs as a user with a name longer than 8 characters")
	}
}

// procUID returns the effective UID of pid from /proc, where there is one,
// as ps shows the effective user.
func procUID(pid int) (string, bool) {
	status, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return "", false
	}
	for line := range strings.Lines(string(status)) {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			if fields := strings.Fields(rest); len(fields) > 1 {
				return fields[1], true
			}
		}
	}
	return "", false
}

// Characters shown as "?" still tell processes apart: a command line that
// changed from one bidirectional control to another is a different process
// to the check before signalling, though both are shown alike.
func TestIdentityKeepsWhatTheScreenReplaces(t *testing.T) {
	a, err := Parse("  1 0 alice /x \u202Ea\n", "  1 0 x\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("  1 0 alice /x \u202Da\n", "  1 0 x\n")
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Raw != b[0].Raw {
		t.Fatalf("Raw = %q and %q, want both shown alike", a[0].Raw, b[0].Raw)
	}
	if a[0].Identity == b[0].Identity {
		t.Errorf("Identity = %q for both, want them to differ", a[0].Identity)
	}
}

// ps on macOS reports some failures on stderr and still exits 0, so
// anything on stderr is a failure, not a missing process.
func TestCurrentReportsAPsThatWritesToStderr(t *testing.T) {
	fakePS(t, "echo 'Failure calling sysctl: Operation not permitted' >&2; exit 0")
	if _, ok, err := Current(os.Getpid()); err == nil || !strings.Contains(err.Error(), "sysctl") {
		t.Errorf("Current() = %v, %v; want an error with what ps wrote", ok, err)
	}
}

// What ps wrote reaches the report as one line with control characters
// shown as "?", so that it can neither send escape sequences to the
// terminal nor forge report lines.
func TestCurrentErrorShowsPsMessageSafely(t *testing.T) {
	fakePS(t, `printf '\033[2J\nfailed: forged report\n' >&2; exit 1`)
	_, _, err := Current(os.Getpid())
	if err == nil {
		t.Fatal("Current() succeeded, want an error")
	}
	if msg := err.Error(); strings.ContainsAny(msg, "\x1b\n") {
		t.Errorf("error = %q, want one line without control characters", msg)
	}
}

// procps cuts user names to 8 characters unless the column is widened, so
// on Linux kil asks for a wider one.
func TestListWidensTheUserColumnOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only procps cuts user names")
	}
	args := filepath.Join(t.TempDir(), "args")
	fakePS(t, `echo "$@" >> '`+args+`'`)
	if _, err := List(0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "user:64=") {
		t.Errorf("ps ran with %q, want user:64=", got)
	}
}

// Only the padding ps adds is trimmed: a command line that gained a
// trailing line separator is a different process to the check before
// signalling.
func TestIdentityKeepsTrailingUnicodeSpaces(t *testing.T) {
	a, err := Parse("  12 1 alice /x\n", "  12 1 x\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("  12 1 alice /x\u2028\n", "  12 1 x\n")
	if err != nil {
		t.Fatal(err)
	}
	if a[0].Identity == b[0].Identity {
		t.Errorf("Identity = %q for both, want them to differ", a[0].Identity)
	}
}

// procps warns about a bogus screen size on stderr and still lists every
// process; kil never needs a screen size from ps, as it asks for whole
// lines, so the warning must not fail the listing.
func TestListIgnoresTheScreenSize(t *testing.T) {
	t.Setenv("LINES", "1")
	t.Setenv("COLUMNS", "1")
	if _, err := List(0); err != nil {
		t.Errorf("List() with a 1x1 screen: %v", err)
	}
}

// With ambiguous-width characters drawn wide, the columns still line up
// on screen.
func TestParseLinesUpAmbiguousWideNames(t *testing.T) {
	width.SetAmbiguousWide(true)
	t.Cleanup(func() { width.SetAmbiguousWide(false) })
	got, err := Parse("  1 0 \u00b7\u00b7 /a\n  2 0 abc /b\n", "  1 0 x\n  2 0 y\n")
	if err != nil {
		t.Fatal(err)
	}
	a := width.String(string([]rune(got[0].Raw)[:got[0].Frozen]))
	b := width.String(string([]rune(got[1].Raw)[:got[1].Frozen]))
	if a != b {
		t.Errorf("frozen columns are %d and %d columns wide, want them equal: %q, %q", a, b, got[0].Raw, got[1].Raw)
	}
}

// CurrentAll checks several processes with one pair of ps runs, rather
// than one pair per process, and leaves out those that are gone.
func TestCurrentAllChecksSeveralTogether(t *testing.T) {
	var pids []int
	for range 3 {
		cmd := exec.Command("sleep", "60")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		pids = append(pids, cmd.Process.Pid)
	}
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	got, err := CurrentAll(append(pids, gone.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("CurrentAll() found %d processes, want the 3 running", len(got))
	}
	for _, pid := range pids {
		want, ok, err := Current(pid)
		if err != nil || !ok || got[pid] != want {
			t.Errorf("CurrentAll()[%d] = %q, want %q as Current gives it", pid, got[pid], want)
		}
	}

	args := filepath.Join(t.TempDir(), "args")
	fakePS(t, `echo "$@" >> '`+args+`'`)
	if _, err := CurrentAll(pids); err != nil {
		t.Fatal(err)
	}
	runs, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(runs), "\n"); n != 2 {
		t.Errorf("ps ran %d times for 3 processes, want 2:\n%s", n, runs)
	}
}
