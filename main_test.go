package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/dotfiles"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

func TestHeader(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nx = \"gh:a/b\"\n", 0o644)
	out := stripANSI(summary())
	for _, want := range []string{"machine", "server", "source", "remote", "not bound", "files", "up to date", "packages", "0/1 installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q:\n%s", want, out)
		}
	}
}

func TestBoxesShareWidth(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nx = \"gh:a/b\"\n", 0o644)
	want := ui.BoxWidth + 2
	for name, out := range map[string]string{
		"help": help(), "small": ui.Box("t", "x"),
		"table": ui.Box("t", ui.Columns([]string{"A", "B"}, [][]string{{"1", "2"}})),
	} {
		for _, l := range strings.Split(out, "\n") {
			if w := lipgloss.Width(l); w != want {
				t.Fatalf("%s: line width %d, want %d: %q", name, w, want, stripANSI(l))
			}
		}
	}
	if w := lipgloss.Width(strings.Split(ui.Box("t", strings.Repeat("y", 100)), "\n")[0]); w != 104 {
		t.Fatalf("box must grow for wide content, got %d", w)
	}
}

func TestMenuSorted(t *testing.T) {
	for i := 1; i < len(menu)-1; i++ {
		if strings.ToLower(menu[i-1][1]) > strings.ToLower(menu[i][1]) {
			t.Errorf("menu not alphabetical: %q before %q", menu[i-1][1], menu[i][1])
		}
	}
	if menu[len(menu)-1][0] != "quit" {
		t.Error("Quit must stay last")
	}
}

// The full-screen view must fit any window: no line wider than it, never taller than it.
func TestLayoutFitsWindow(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nx = \"gh:a/b\"\n", 0o644)
	write(t, filepath.Join(src, "dot_rc"), "x", 0o644)
	defer func(w int) { ui.BoxWidth = w }(ui.BoxWidth)
	for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {160, 50}, {240, 70}} {
		var tm tea.Model = newModel()
		tm, _ = tm.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		tm, _ = tm.Update(capture("Status", "status", tm.(model).reportWidth(), dotfiles.Status)()) // a real report
		check := func(what string) {
			lines := strings.Split(tm.View(), "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], what, len(lines))
			}
			for _, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%dx%d %s: line %d wide: %q", size[0], size[1], what, w, stripANSI(l))
					break
				}
			}
		}
		check("menu")
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEnter}) // first entry: Add file opens a form
		check("form")
		if tm.(model).form == nil {
			t.Fatalf("%dx%d: enter on Add file should open a form", size[0], size[1])
		}
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if tm.(model).form != nil || tm.(model).content {
			t.Fatal("esc must close the form and go back to the menu")
		}
	}
}

// A resize that reruns a report while another is still running must not leave ui.Out on a dead buffer
// (that swallowed the progress output of the next install).
func TestOverlappingCaptures(t *testing.T) {
	// the first report finishes before the second: that order used to leave ui.Out on the first one's buffer
	slow := capture("A", "", 50, func() error { time.Sleep(30 * time.Millisecond); ui.Say(ui.OK, "·", "a", "x"); return nil })
	later := capture("B", "", 60, func() error { time.Sleep(60 * time.Millisecond); return nil })
	var wg sync.WaitGroup
	wg.Add(2)
	var a, b tea.Msg
	go func() { a = slow(); wg.Done() }()
	time.Sleep(10 * time.Millisecond)
	go func() { b = later(); wg.Done() }()
	wg.Wait()
	if ui.Out != io.Writer(os.Stdout) || ui.BoxWidth != 78 {
		t.Fatalf("shared output/width not restored: %T %d", ui.Out, ui.BoxWidth)
	}
	if !strings.Contains(a.(contentMsg).body, "a") || strings.Contains(b.(contentMsg).body, "a ") {
		t.Fatalf("reports mixed up: %q / %q", a.(contentMsg).body, b.(contentMsg).body)
	}
}

// Resize events are debounced: only the settled width redraws the report, and a height-only change none.
func TestResizeDebounce(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "", 0o644)
	var tm tea.Model = newModel()
	tm, cmd := tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd == nil {
		t.Fatal("first size must draw the report right away")
	}
	tm, _ = tm.Update(cmd())
	tm, cmd = tm.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if _, waits := cmd().(resizedMsg); !waits {
		t.Fatal("a resize during use must wait for the width to settle, not redraw at once")
	}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 130, Height: 30}) // still dragging
	if _, rerun := tm.Update(resizedMsg{120}); rerun != nil {
		t.Fatal("a width the window already left must not redraw")
	}
	if _, rerun := tm.Update(resizedMsg{130}); rerun == nil {
		t.Fatal("the settled width must redraw")
	}
	m := tm.(model)
	tm, _ = tm.Update(m.rerun()()) // draw at 130
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	if _, rerun := tm.Update(resizedMsg{130}); rerun != nil {
		t.Fatal("a height-only change must not redraw the report")
	}
}
