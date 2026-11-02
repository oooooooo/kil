package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oooooooo/kil/internal/picker"
)

func items(lines ...string) []picker.Item {
	out := make([]picker.Item, len(lines))
	for i, l := range lines {
		out[i] = picker.Item{ID: l, Text: l}
	}
	return out
}

func noReload() ([]picker.Item, error) { return nil, nil }

func press(m Model, keys ...tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(k)
		m = next.(Model)
	}
	return m, cmd
}

func typed(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func key(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

func sized(m Model) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return next.(Model)
}

func targetTexts(m Model) []string {
	var out []string
	for _, it := range m.Targets() {
		out = append(out, it.Text)
	}
	return out
}

func TestTypingFiltersTheView(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("pyt"))
	view := m.View()
	if !strings.Contains(view, "202 python b") || strings.Contains(view, "101 node a") {
		t.Errorf("view after typing \"pyt\":\n%s", view)
	}
}

func TestInitialQueryIsApplied(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	if strings.Contains(m.View(), "202 python b") {
		t.Errorf("initial query not applied:\n%s", m.View())
	}
}

func TestBackspaceAndCtrlUEditTheQuery(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, _ = press(m, typed("pyx"), key(tea.KeyBackspace))
	if m.Query() != "py" {
		t.Errorf("Query() after backspace = %q, want \"py\"", m.Query())
	}
	m, _ = press(m, key(tea.KeyCtrlU))
	if m.Query() != "" {
		t.Errorf("Query() after Ctrl-U = %q, want empty", m.Query())
	}
}

func TestSelectionStaysVisibleAfterQueryChange(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "node", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyTab), key(tea.KeyCtrlU), typed("python"))
	view := m.View()
	if !strings.Contains(view, "101 node a") {
		t.Errorf("selected node row is not shown after changing the query:\n%s", view)
	}
}

func TestEnterAcceptsTargets(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b"), "", "SIGKILL", noReload))
	m, cmd := press(m, key(tea.KeyDown), key(tea.KeyEnter))
	if m.Outcome() != Accepted {
		t.Fatalf("Outcome() = %v, want Accepted", m.Outcome())
	}
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b"}) {
		t.Errorf("Targets() = %v", got)
	}
	if cmd == nil {
		t.Error("Enter did not quit")
	}
}

func TestEnterWithNothingShownSelectsNothing(t *testing.T) {
	m := sized(New(items("101 node a"), "zzz", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyEnter))
	if m.Outcome() != NothingSelected {
		t.Errorf("Outcome() = %v, want NothingSelected", m.Outcome())
	}
}

func TestEscAndCtrlCAbort(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := sized(New(items("101 node a"), "", "SIGKILL", noReload))
		m, _ = press(m, key(k))
		if m.Outcome() != Aborted {
			t.Errorf("Outcome() after %v = %v, want Aborted", k, m.Outcome())
		}
		if len(m.Targets()) != 0 {
			t.Errorf("Targets() after %v = %v, want none", k, m.Targets())
		}
	}
}

func TestCtrlASelectsEveryShownRow(t *testing.T) {
	m := sized(New(items("101 node a", "202 python b", "203 python c"), "python", "SIGKILL", noReload))
	m, _ = press(m, key(tea.KeyCtrlA), key(tea.KeyEnter))
	if got := targetTexts(m); !slices.Equal(got, []string{"202 python b", "203 python c"}) {
		t.Errorf("Targets() = %v", got)
	}
}

func TestCtrlRReloadsTheList(t *testing.T) {
	reload := func() ([]picker.Item, error) { return items("101 node a", "404 go d"), nil }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, cmd := press(m, key(tea.KeyCtrlR))
	if cmd == nil {
		t.Fatal("Ctrl-R returned no command")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.View(), "404 go d") {
		t.Errorf("reloaded row not shown:\n%s", m.View())
	}
}

func TestFailedReloadEndsWithoutTargets(t *testing.T) {
	reload := func() ([]picker.Item, error) { return nil, errors.New("ps failed") }
	m := sized(New(items("101 node a"), "", "SIGKILL", reload))
	m, _ = press(m, key(tea.KeyTab))
	m, cmd := press(m, key(tea.KeyCtrlR))
	next, quit := m.Update(cmd())
	m = next.(Model)
	if m.Outcome() != ListFailed || m.Err() == nil {
		t.Errorf("Outcome() = %v, Err() = %v; want ListFailed and an error", m.Outcome(), m.Err())
	}
	if len(m.Targets()) != 0 {
		t.Errorf("Targets() = %v, want none", m.Targets())
	}
	if quit == nil {
		t.Error("failed reload did not quit")
	}
}

func TestHeaderNamesTheSignal(t *testing.T) {
	m := sized(New(items("101 node a"), "", "SIGTERM", noReload))
	if !strings.Contains(m.View(), "SIGTERM") {
		t.Errorf("view does not name the signal:\n%s", m.View())
	}
}

func TestViewScrollsToKeepTheCursorVisible(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "proc line "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	m := sized(New(items(lines...), "", "SIGKILL", noReload))
	for range 40 {
		m, _ = press(m, key(tea.KeyDown))
	}
	view := m.View()
	if !strings.Contains(view, lines[40]) {
		t.Errorf("cursor row %q not in view:\n%s", lines[40], view)
	}
	if n := strings.Count(view, "\n") + 1; n > 20 {
		t.Errorf("view has %d lines, want at most the window height 20", n)
	}
}

func TestMovingUpInsideTheWindowDoesNotScroll(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "proc line "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	m := sized(New(items(lines...), "", "SIGKILL", noReload))
	for range 40 {
		m, _ = press(m, key(tea.KeyDown))
	}
	firstRow := func(m Model) string { return strings.Split(m.View(), "\n")[2] }
	before := firstRow(m)
	m, _ = press(m, key(tea.KeyUp))
	if after := firstRow(m); after != before {
		t.Errorf("first row changed from %q to %q after moving up inside the window", before, after)
	}
}
