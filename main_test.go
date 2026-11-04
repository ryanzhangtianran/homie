package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/ryanzhangtianran/homie/internal/ui"
)

func TestHeader(t *testing.T) {
	_, src := sandbox(t, "server")
	write(t, filepath.Join(src, ".homie.toml"), "[packages]\nx = \"gh:a/b\"\n", 0o644)
	out := stripANSI(header())
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
		"help": help(), "header": header(), "small": ui.Box("t", "x"),
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
